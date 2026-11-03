package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/embed"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/semcache"
)

// recordingShadow captures offered samples.
type recordingShadow struct {
	mu      sync.Mutex
	samples []semcache.Sample
}

func (r *recordingShadow) Offer(s semcache.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, s)
}

func (r *recordingShadow) all() []semcache.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]semcache.Sample(nil), r.samples...)
}

func shadowServer(t *testing.T, p provider.Provider, shadow ShadowCache) *Server {
	t.Helper()
	pipeline := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil))
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Shadow: shadow, Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions)
}

func TestShadowOfferedForBufferedSuccess(t *testing.T) {
	shadow := &recordingShadow{}
	s := shadowServer(t, okProvider(), shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/fast","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello there"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	samples := shadow.all()
	if len(samples) != 1 {
		t.Fatalf("offered %d samples, want 1", len(samples))
	}
	sm := samples[0]
	if sm.Prompt != "hello there" {
		t.Fatalf("prompt = %q", sm.Prompt)
	}
	if sm.Namespace != routeKey("be brief\n", "none", "none").Namespace() {
		t.Fatalf("namespace = %q, want tenant+model+system derivation", sm.Namespace)
	}
	if sm.Fingerprint == "" || sm.RequestID == "" {
		t.Fatalf("sample = %+v", sm)
	}
}

func TestShadowOfferedAtStreamEndWithDoneGate(t *testing.T) {
	shadow := &recordingShadow{}
	p := okProvider()
	p.sse = fakeSSE
	s := shadowServer(t, p, shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	samples := shadow.all()
	if len(samples) != 1 {
		t.Fatalf("offered %d samples, want 1 at stream end", len(samples))
	}
	// Fingerprint covers the scanned deltas ("Hello world").
	if samples[0].Fingerprint != semcache.Fingerprint("Hello world") {
		t.Fatalf("fingerprint = %q, want hash of streamed content", samples[0].Fingerprint)
	}
}

func TestShadowNotOfferedForAbortedStream(t *testing.T) {
	shadow := &recordingShadow{}
	p := okProvider()
	p.sse = fakeSSE
	pipeline := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil))
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Shadow: shadow, Logger: discardLogger()})

	resp, err := pipeline(h)(t.Context(), &core.Request{Op: "chat.completions", Payload: streamingPayload()})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Stream.Close() // abandoned before [DONE]

	if got := len(shadow.all()); got != 0 {
		t.Fatalf("offered %d samples for an aborted stream, want 0", got)
	}
}

func TestShadowNotOfferedForMultiTurn(t *testing.T) {
	shadow := &recordingShadow{}
	s := shadowServer(t, okProvider(), shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/fast","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := len(shadow.all()); got != 0 {
		t.Fatalf("offered %d samples for multi-turn, want 0", got)
	}
}

func TestShadowNotOfferedOnFailure(t *testing.T) {
	shadow := &recordingShadow{}
	p := okProvider()
	p.err = core.NewError(core.CodeUpstream, "down")
	s := shadowServer(t, p, shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := len(shadow.all()); got != 0 {
		t.Fatalf("offered %d samples for a failed request, want 0", got)
	}
}

func TestNilShadowIsSilent(t *testing.T) {
	s := shadowServer(t, okProvider(), nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCacheableShapes(t *testing.T) {
	tests := []struct {
		name string
		msgs []provider.Message
		ok   bool
	}{
		{"single user", []provider.Message{{Role: "user", Content: "q"}}, true},
		{"system then user", []provider.Message{{Role: "system", Content: "s"}, {Role: "user", Content: "q"}}, true},
		{"two users", []provider.Message{{Role: "user", Content: "a"}, {Role: "user", Content: "b"}}, false},
		{"assistant turn", []provider.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}, false},
		{"system after user", []provider.Message{{Role: "user", Content: "a"}, {Role: "system", Content: "s"}}, false},
		{"no user", []provider.Message{{Role: "system", Content: "s"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := cacheable(tt.msgs)
			if ok != tt.ok {
				t.Fatalf("cacheable = %v, want %v", ok, tt.ok)
			}
		})
	}
}

func routeKey(systemPrompt, temperature, maxTokens string) semcache.Key {
	return semcache.Key{Tenant: DevTenant, RouteModel: "mock-model", ReportedModel: "mock-model",
		SystemPrompt: systemPrompt, Temperature: temperature, MaxTokens: maxTokens}
}

func reportingSSE(model string) string {
	if model == "" {
		return fakeSSE
	}
	return strings.ReplaceAll(fakeSSE, `{"object":"chat.completion.chunk",`, `{"object":"chat.completion.chunk","model":"`+model+`",`)
}

type exchange struct {
	stream    bool
	maxTokens string
	reported  string
}

func (e exchange) post(t *testing.T, s *Server, p *fakeProvider) *httptest.ResponseRecorder {
	t.Helper()
	p.resp.Model, p.sse = e.reported, reportingSSE(e.reported)
	body := fmt.Sprintf(`{"model":"pusaq/fast","stream":%t,"messages":[{"role":"user","content":"how do I cancel my subscription"}]`, e.stream)
	if e.maxTokens != "" {
		body += `,"max_tokens":` + e.maxTokens
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body+"}")))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return rec
}

func TestShadowNamespaceKeysOnReportedModelAndMaxTokens(t *testing.T) {
	for _, tt := range []struct {
		name     string
		ex       exchange
		reported string
		max      string
	}{
		{"buffered reported", exchange{reported: "mock-model-2026-10"}, "mock-model-2026-10", "none"},
		{"buffered unreported", exchange{}, "mock-model", "none"},
		{"streamed reported", exchange{stream: true, reported: "mock-model-2026-10"}, "mock-model-2026-10", "none"},
		{"streamed unreported", exchange{stream: true}, "mock-model", "none"},
		{"buffered max_tokens", exchange{maxTokens: "32"}, "mock-model", "32"},
		{"streamed max_tokens", exchange{stream: true, maxTokens: "64"}, "mock-model", "64"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			shadow := &recordingShadow{}
			p := okProvider()
			tt.ex.post(t, shadowServer(t, p, shadow), p)
			samples := shadow.all()
			if len(samples) != 1 {
				t.Fatalf("offered %d samples, want 1", len(samples))
			}
			want := routeKey("", "none", tt.max)
			want.ReportedModel = tt.reported
			if samples[0].Namespace != want.Namespace() {
				t.Fatalf("namespace = %q, want %q", samples[0].Namespace, want.Namespace())
			}
		})
	}
}

type shadowRecords struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *shadowRecords) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *shadowRecords) neighbors() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Count(r.buf.String(), `"msg":"cache_shadow"`)
}

func TestShadowNeighborsStayWithinNamespace(t *testing.T) {
	for _, tt := range []struct {
		name          string
		first, second exchange
		neighbors     int
	}{
		{"identical requests", exchange{maxTokens: "32"}, exchange{maxTokens: "32"}, 1},
		{"max_tokens absent and set", exchange{}, exchange{maxTokens: "32"}, 0},
		{"different max_tokens", exchange{maxTokens: "32"}, exchange{maxTokens: "64"}, 0},
		{"different reported models", exchange{reported: "mock-model-2026-05"}, exchange{reported: "mock-model-2026-10"}, 0},
		{"different streamed reported models", exchange{stream: true, reported: "mock-model-2026-05"}, exchange{stream: true, reported: "mock-model-2026-10"}, 0},
		{"unreported meets the route model", exchange{reported: "mock-model"}, exchange{stream: true}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			records := &shadowRecords{}
			shadow := semcache.NewShadow(embed.Local{}, semcache.NewIndex(16), slog.New(slog.NewJSONHandler(records, nil)), 0.9, 1, 4)
			p := okProvider()
			s := shadowServer(t, p, shadow)
			tt.first.post(t, s, p)
			tt.second.post(t, s, p)
			shadow.Close()
			if got := records.neighbors(); got != tt.neighbors {
				t.Fatalf("neighbors = %d, want %d", got, tt.neighbors)
			}
		})
	}
}

func TestHeaderCarriesRouteModelAndBodyTheReportedOne(t *testing.T) {
	for _, stream := range []bool{false, true} {
		p := okProvider()
		rec := exchange{stream: stream, reported: "mock-model-2026-10"}.post(t, shadowServer(t, p, nil), p)
		if got := rec.Header().Get(ServedModelHeader); got != "mock-model" {
			t.Fatalf("stream=%v: %s = %q, want the route's physical model", stream, ServedModelHeader, got)
		}
		if stream {
			if !strings.Contains(rec.Body.String(), `"model":"mock-model-2026-10"`) {
				t.Fatalf("streamed chunks lost the provider's model: %s", rec.Body.String())
			}
			continue
		}
		var body completionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "mock-model-2026-10" {
			t.Fatalf("body model = %q, want the provider-reported name", body.Model)
		}
	}
}
