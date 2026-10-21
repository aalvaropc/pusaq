package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
)

type settlementTokenLimiter struct {
	fakeTokenLimiter
	onCharge             func()
	adjustErr            error
	adjustmentContextErr error
	adjustmentBudget     time.Duration
}

func (l *settlementTokenLimiter) Allow(ctx context.Context, tenant string, cost float64) (ratelimit.Decision, error) {
	d, err := l.fakeTokenLimiter.Allow(ctx, tenant, cost)
	if err == nil && d.Allowed && l.onCharge != nil {
		l.onCharge()
	}
	return d, err
}

func (l *settlementTokenLimiter) Adjust(ctx context.Context, tenant string, delta float64) error {
	l.adjustmentContextErr = ctx.Err()
	if deadline, ok := ctx.Deadline(); ok {
		l.adjustmentBudget = time.Until(deadline)
	}
	_ = l.fakeTokenLimiter.Adjust(ctx, tenant, delta)
	return l.adjustErr
}

func unattemptedTokenServer(t *testing.T, tokens TokenLimiter, logger *slog.Logger) (*Server, *[]core.CostEvent, []*scriptedProvider) {
	t.Helper()
	brs, breakers := tightBreakers(t)
	for _, br := range breakers {
		br.Record(false)
		br.Record(false)
	}
	providers := []*scriptedProvider{{name: "primary"}, {name: "backup"}}
	var events []core.CostEvent
	pipeline := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, logger), RequestID(), Auth(nil))
	h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{"primary": providers[0], "backup": providers[1]},
		CompletionsOptions{Breakers: brs, Tokens: tokens, Prices: testPrices(), ReserveDefault: 100, Logger: logger})
	return NewServer(logger, pipeline, h), &events, providers
}

func TestUnattemptedTokenReservationRefunded(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			l := &settlementTokenLimiter{fakeTokenLimiter: fakeTokenLimiter{allowed: true}}
			s, events, providers := unattemptedTokenServer(t, l, discardLogger())
			body := strings.TrimSuffix(string(validPayload()), "}") + `,"stream":` + strconv.FormatBool(stream) + `}`
			if rec := post(s, body); rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", rec.Code)
			}
			if len(l.charges) != 1 || l.charges[0] != 35 || len(l.adjusts) != 1 || l.adjusts[0] != 35 {
				t.Fatalf("charges = %v adjusts = %v, want [35] and [35]", l.charges, l.adjusts)
			}
			if providers[0].calls+providers[1].calls != 0 || len(*events) != 1 {
				t.Fatalf("provider calls = %d/%d events = %d", providers[0].calls, providers[1].calls, len(*events))
			}
			if e := (*events)[0]; e.Attempts != 0 || e.TokensCharged != 35 || e.Cost.USD != 0 || e.ErrorCode != core.CodeUpstream {
				t.Fatalf("event = %+v", e)
			}
		})
	}
}

func TestUnattemptedCancellationRefundUsesIndependentContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &settlementTokenLimiter{fakeTokenLimiter: fakeTokenLimiter{allowed: true}, onCharge: cancel}
	p := okProvider()
	s, events := tokenServer(t, p, l)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 499 || (*events)[0].Attempts != 0 || p.got.Model != "" {
		t.Fatalf("status = %d event = %+v provider = %+v", rec.Code, (*events)[0], p.got)
	}
	if len(l.adjusts) != 1 || l.adjusts[0] != 35 {
		t.Fatalf("adjusts = %v, want [35]", l.adjusts)
	}
	if l.adjustmentContextErr != nil || l.adjustmentBudget <= 0 || l.adjustmentBudget > limiterBudget {
		t.Fatalf("adjustment context = %v, budget = %v", l.adjustmentContextErr, l.adjustmentBudget)
	}
}

func TestUnattemptedRefundFailurePreservesOutcome(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	l := &settlementTokenLimiter{fakeTokenLimiter: fakeTokenLimiter{allowed: true}, adjustErr: errors.New("refund unavailable")}
	s, events, _ := unattemptedTokenServer(t, l, logger)
	if rec := post(s, string(validPayload())); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want original 502", rec.Code)
	}
	if len(l.adjusts) != 1 || l.adjusts[0] != 35 || len(*events) != 1 || (*events)[0].ErrorCode != core.CodeUpstream {
		t.Fatalf("adjusts = %v events = %+v", l.adjusts, *events)
	}
	if !strings.Contains(logs.String(), "token reconciliation failed") || !strings.Contains(logs.String(), "refund unavailable") {
		t.Fatalf("missing reconciliation warning: %s", logs.String())
	}
}

func TestUnconfirmedTokenReservationsAreNotRefunded(t *testing.T) {
	for _, storeFailure := range []bool{false, true} {
		t.Run(strconv.FormatBool(storeFailure), func(t *testing.T) {
			l := &settlementTokenLimiter{}
			want := http.StatusTooManyRequests
			if storeFailure {
				l.allowErr = errors.New("store unavailable")
				want = http.StatusOK
			}
			s, events := tokenServer(t, okProvider(), l)
			if rec := post(s, string(validPayload())); rec.Code != want {
				t.Fatalf("status = %d, want %d", rec.Code, want)
			}
			if len(l.adjusts) != 0 || len(*events) != 1 || (*events)[0].TokensCharged != 0 {
				t.Fatalf("adjusts = %v events = %+v", l.adjusts, *events)
			}
		})
	}
}

type panickingTokenProvider struct{ provider.Provider }

func (p panickingTokenProvider) Complete(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	panic("provider panic")
}

func (p panickingTokenProvider) StreamComplete(context.Context, provider.ChatRequest) (io.ReadCloser, error) {
	panic("provider panic")
}

func TestAttemptedUnknownUsageKeepsReservation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, failure := range []string{"error", "timeout", "panic"} {
			t.Run(strconv.FormatBool(stream)+"/"+failure, func(t *testing.T) {
				l := &settlementTokenLimiter{fakeTokenLimiter: fakeTokenLimiter{allowed: true}}
				p := okProvider()
				p.err = core.NewError(core.CodeUpstream, "upstream failed")
				if failure == "timeout" {
					p.err = &core.Error{Code: core.CodeUpstream, Message: "upstream timeout", Err: context.DeadlineExceeded}
				}
				var upstream provider.Provider = p
				if failure == "panic" {
					upstream = panickingTokenProvider{Provider: p}
				}
				s, events := tokenServer(t, upstream, l)
				body := strings.TrimSuffix(string(validPayload()), "}") + `,"stream":` + strconv.FormatBool(stream) + `}`
				rec := post(s, body)
				want := http.StatusBadGateway
				if failure == "panic" {
					want = http.StatusInternalServerError
				}
				if rec.Code != want || len(l.charges) != 1 || len(l.adjusts) != 0 || len(*events) != 1 || (*events)[0].Attempts != 1 {
					t.Fatalf("status = %d charges = %v adjusts = %v events = %+v", rec.Code, l.charges, l.adjusts, *events)
				}
			})
		}
	}
}

func TestFallbackTokenReservationReconcilesOnce(t *testing.T) {
	l := &settlementTokenLimiter{fakeTokenLimiter: fakeTokenLimiter{allowed: true}}
	primary := &scriptedProvider{name: "primary", fail: true}
	backup := &scriptedProvider{name: "backup"}
	var events []core.CostEvent
	pipeline := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, discardLogger()), RequestID(), Auth(nil))
	h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{"primary": primary, "backup": backup},
		CompletionsOptions{Tokens: l, Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, h)
	if rec := post(s, string(validPayload())); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(l.adjusts) != 1 || l.adjusts[0] != 30 || len(events) != 1 || events[0].Attempts != 2 {
		t.Fatalf("adjusts = %v events = %+v", l.adjusts, events)
	}
}

func TestUnattemptedReservationsPreserveFrozenStoreBalance(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.SetTime(time.UnixMilli(1_700_000_000_000))
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	l, err := ratelimit.New(client, "tpm", ratelimit.Limits{Burst: 1000, Rate: 1, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	s, events, providers := unattemptedTokenServer(t, l, discardLogger())
	for i := range 100 {
		if rec := post(s, string(validPayload())); rec.Code != http.StatusBadGateway {
			t.Fatalf("request %d status = %d, want 502 without consuming quota", i, rec.Code)
		}
	}
	remaining, err := strconv.ParseFloat(mr.HGet("rl:{dev}:tpm", "tokens"), 64)
	if err != nil || remaining != 1000 || len(*events) != 100 || providers[0].calls+providers[1].calls != 0 {
		t.Fatalf("remaining = %v, %v events = %d calls = %d/%d", remaining, err, len(*events), providers[0].calls, providers[1].calls)
	}
	t.Logf("100 reservations, 0 provider attempts, final balance %.0f/1000", remaining)
}
