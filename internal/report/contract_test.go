package report_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/embed"
	"github.com/aalvaropc/pusaq/internal/gateway"
	"github.com/aalvaropc/pusaq/internal/pricing"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
	"github.com/aalvaropc/pusaq/internal/report"
	"github.com/aalvaropc/pusaq/internal/semcache"
	"github.com/aalvaropc/pusaq/internal/telemetry"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

type scripted struct {
	mu    sync.Mutex
	calls int
	plan  []string
}

func (s *scripted) next() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= len(s.plan) {
		return s.plan[s.calls-1]
	}
	return "allow"
}

func (s *scripted) Allow(context.Context, string, float64) (ratelimit.Decision, error) {
	switch s.next() {
	case "fail":
		return ratelimit.Decision{}, errors.New("store down")
	case "deny":
		return ratelimit.Decision{Allowed: false}, nil
	default:
		return ratelimit.Decision{Allowed: true}, nil
	}
}

func (s *scripted) Adjust(context.Context, string, float64) error { return nil }

func (s *scripted) Limits() ratelimit.Limits { return ratelimit.Limits{Burst: 1e6, Rate: 1e6} }

type backend struct {
	name string
	fail bool
}

func (b backend) Name() string { return b.name }

func (b backend) Complete(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	if b.fail {
		return provider.ChatResponse{}, core.NewError(core.CodeUpstream, "upstream down")
	}
	return provider.ChatResponse{
		ID:      "c",
		Model:   "model-x",
		Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: "same answer"}}},
		Usage:   core.Usage{PromptTokens: 4, CompletionTokens: 6},
	}, nil
}

func (b backend) StreamComplete(context.Context, provider.ChatRequest) (io.ReadCloser, error) {
	return nil, core.NewError(core.CodeUpstream, "streaming not scripted")
}

func TestReportReadsWhatTheGatewayWrites(t *testing.T) {
	var logs lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	emitter := telemetry.NewEmitter(telemetry.SlogSink{Logger: logger}, 64)
	shadow := semcache.NewShadow(embed.Local{}, semcache.NewIndex(100), logger, 0.95, 1, 16)

	requests := &scripted{plan: []string{"fail", "deny", "allow", "allow"}}
	tokens := &scripted{plan: []string{"fail", "deny", "allow"}}
	primaryBreaker := breaker.New(breaker.Config{Window: 1, MinSamples: 1})
	completions := gateway.CompletionsHandler(
		map[string]gateway.Route{"pusaq/fast": {Provider: "primary", Model: "model-x", Fallbacks: []string{"backup"}}},
		map[string]provider.Provider{"primary": backend{name: "primary", fail: true}, "backup": backend{name: "backup"}},
		gateway.CompletionsOptions{
			Tokens:         tokens,
			ReserveDefault: 10,
			Breakers: func(name string) *breaker.Breaker {
				if name == "primary" {
					return primaryBreaker
				}
				return nil
			},
			Shadow: shadow,
			Prices: pricing.NewTable("contract-v1", map[string]pricing.Price{"model-x": {InputPerM: 1, OutputPerM: 2}}),
			Logger: logger,
		})
	pipeline := core.Chain(
		gateway.Accounting(emitter.Emit, logger),
		gateway.RequestID(),
		gateway.Auth(map[string]string{"key-1": "tenant-1"}),
		gateway.RateLimit(requests, nil, logger),
	)
	srv := gateway.NewServer(logger, pipeline, completions)
	srv.SetReady(true)

	body := `{"model":"pusaq/fast","messages":[{"role":"user","content":"hello there"}],"metadata":{"run_id":"contract"}}`
	var statuses []int
	for range 4 {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer key-1")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		statuses = append(statuses, rec.Code)
	}
	emitter.Close()
	shadow.Close()
	if !slices.Equal(statuses, []int{200, 429, 429, 200}) {
		t.Fatalf("statuses = %v", statuses)
	}

	logs.mu.Lock()
	raw := logs.buf.String()
	logs.mu.Unlock()
	in, err := report.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if in.Malformed != 0 || len(in.Events) != 4 {
		t.Fatalf("malformed %d events %d in:\n%s", in.Malformed, len(in.Events), raw)
	}
	r, err := report.Build(in, report.Options{RunID: "contract"})
	if err != nil {
		t.Fatal(err)
	}

	limits := r.Limits.Total
	if limits.RequestLimited != 1 || limits.TokenLimited != 1 || limits.FailOpenRequest != 1 || limits.FailOpenToken != 1 {
		t.Fatalf("limits = %+v", limits)
	}
	wantBackends := []report.BackendRow{
		{Backend: "primary", FirstChoice: 2, Skipped: 1, Failed: 1},
		{Backend: "backup", Served: 2, AfterFallback: 2},
	}
	if !slices.Equal(r.Fallback.Backends, wantBackends) || !slices.Equal(r.Fallback.Attempts, []report.AttemptCount{{1, 1}, {2, 1}}) {
		t.Fatalf("fallback = %+v attempts %+v", r.Fallback.Backends, r.Fallback.Attempts)
	}
	tenant := r.Cost.Tenants[0]
	if len(r.Cost.Tenants) != 1 || tenant.Tenant != "tenant-1" || tenant.Requests != 4 || tenant.Measured != 2 ||
		tenant.PromptTokens != 8 || tenant.CompletionTokens != 12 || tenant.MeasuredUSD <= 0 {
		t.Fatalf("cost = %+v", r.Cost.Tenants)
	}
	if !slices.Equal(r.Cost.PriceVersions, []string{"contract-v1"}) || r.Latency.Routes[0].Alias != "pusaq/fast" || r.Latency.Routes[0].Count != 2 {
		t.Fatalf("versions %v routes %+v", r.Cost.PriceVersions, r.Latency.Routes)
	}
	s := r.Shadow
	if s.Records != 1 || s.Eligible != 2 || s.HitRate != (report.Ratio{Num: 1, Den: 2}) || s.FalsePos != (report.Ratio{Num: 0, Den: 1}) ||
		!slices.Equal(s.Embedders, []string{"local"}) {
		t.Fatalf("shadow = %+v", s)
	}
}
