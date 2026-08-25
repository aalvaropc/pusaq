package gateway

import (
	"context"
	"errors"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
)

// fakeTokenLimiter records the charge and reconciliation traffic.
type fakeTokenLimiter struct {
	allowed    bool
	retryAfter time.Duration
	allowErr   error

	charges []float64
	adjusts []float64
}

func (f *fakeTokenLimiter) Allow(_ context.Context, _ string, cost float64) (ratelimit.Decision, error) {
	if f.allowErr != nil {
		return ratelimit.Decision{}, f.allowErr
	}
	f.charges = append(f.charges, cost)
	return ratelimit.Decision{Allowed: f.allowed, RetryAfter: f.retryAfter}, nil
}

func (f *fakeTokenLimiter) Adjust(_ context.Context, _ string, delta float64) error {
	f.adjusts = append(f.adjusts, delta)
	return nil
}

// tokenServer builds a full pipeline server with TPM enabled.
func tokenServer(t *testing.T, p provider.Provider, tokens TokenLimiter) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), Auth(nil))
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Tokens: tokens, ReserveDefault: 100, Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions), &events
}

func TestTokenChargeAndRefundOnBufferedSuccess(t *testing.T) {
	tl := &fakeTokenLimiter{allowed: true}
	s, events := tokenServer(t, okProvider(), tl)

	// Payload input estimate: chars("user"+"hello")/4 = ceil(9/4) = 3.
	// max_tokens 32 → charge = 3 + 32 = 35. Actual usage: 5 + 2 = 7.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(tl.charges) != 1 || tl.charges[0] != 35 {
		t.Fatalf("charges = %v, want [35]", tl.charges)
	}
	if len(tl.adjusts) != 1 || tl.adjusts[0] != 28 { // 35 charged − 7 actual
		t.Fatalf("adjusts = %v, want [28]", tl.adjusts)
	}
	if len(*events) != 1 || (*events)[0].TokensCharged != 35 {
		t.Fatalf("events = %+v, want tokens_charged 35", *events)
	}
}

func TestTokenDefaultReservationWhenMaxTokensAbsent(t *testing.T) {
	tl := &fakeTokenLimiter{allowed: true}
	s, _ := tokenServer(t, okProvider(), tl)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// input 3 + ReserveDefault 100.
	if len(tl.charges) != 1 || tl.charges[0] != 103 {
		t.Fatalf("charges = %v, want [103]", tl.charges)
	}
}

func TestTokenDenialContract(t *testing.T) {
	tl := &fakeTokenLimiter{allowed: false, retryAfter: 900 * time.Millisecond}
	p := okProvider()
	s, events := tokenServer(t, p, tl)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if p.got.Model != "" {
		t.Fatal("provider called despite token denial")
	}
	if len(tl.adjusts) != 0 {
		t.Fatalf("adjusts = %v, want none for a denied request", tl.adjusts)
	}
	if len(*events) != 1 || (*events)[0].ErrorCode != core.CodeRateLimited {
		t.Fatalf("events = %+v", *events)
	}
}

func TestTokenReconcileOnUpstreamError(t *testing.T) {
	tl := &fakeTokenLimiter{allowed: true}
	p := okProvider()
	p.err = core.NewError(core.CodeUpstream, "upstream provider returned status 500")
	s, _ := tokenServer(t, p, tl)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	// No usage was ever known → the charge stands, no reconciliation.
	if len(tl.adjusts) != 0 {
		t.Fatalf("adjusts = %v, want none when usage is unknown", tl.adjusts)
	}
}

func TestTokenReconcileAtStreamEnd(t *testing.T) {
	tl := &fakeTokenLimiter{allowed: true}
	p := okProvider()
	p.sse = fakeSSE // usage chunk: 9 + 4 = 13 actual
	s, events := tokenServer(t, p, tl)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// streamingPayload has no max_tokens: input 3 + reserve 100 = 103.
	// Reconcile: 103 − 13 = 90, and only after the stream fully drained.
	if len(tl.adjusts) != 1 || tl.adjusts[0] != 90 {
		t.Fatalf("adjusts = %v, want [90] at stream end", tl.adjusts)
	}
	if len(*events) != 1 || (*events)[0].TokensCharged != 103 {
		t.Fatalf("events = %+v", *events)
	}
}

func TestTokenAbandonedStreamKeepsCharge(t *testing.T) {
	tl := &fakeTokenLimiter{allowed: true}
	p := okProvider()
	p.sse = fakeSSE
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), Auth(nil))
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Tokens: tl, ReserveDefault: 100, Logger: discardLogger()})

	resp, err := pipeline(h)(context.Background(), &core.Request{Op: "chat.completions", Payload: streamingPayload()})
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}
	// The client leaves before reading anything: no usage was scanned.
	_ = resp.Stream.Close()

	if len(tl.adjusts) != 0 {
		t.Fatalf("adjusts = %v, want none for an abandoned stream without usage", tl.adjusts)
	}
	if len(events) != 1 || events[0].ErrorCode != core.CodeStreamInterrupted {
		t.Fatalf("events = %+v", events)
	}
}

func TestTokenLimiterStoreFailureFailsOpen(t *testing.T) {
	tl := &fakeTokenLimiter{allowErr: errors.New("connection refused")}
	s, _ := tokenServer(t, okProvider(), tl)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail-open)", rec.Code)
	}
}

func TestEstimateInputTokens(t *testing.T) {
	msgs := []provider.Message{{Role: "user", Content: "hello"}} // 9 chars
	if got := estimateInputTokens(msgs); got != 3 {
		t.Fatalf("estimate = %d, want ceil(9/4)=3", got)
	}
	if got := estimateInputTokens(nil); got != 0 {
		t.Fatalf("estimate(nil) = %d", got)
	}
}

func TestFinalizersRunExactlyOnceOnPanic(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	runs := 0
	terminal := func(_ context.Context, req *core.Request) (*core.Response, error) {
		req.OnFinalize(func() { runs++ })
		panic("boom after registration")
	}
	_, err := core.Chain(Accounting(emit, discardLogger()), RequestID())(terminal)(context.Background(), &core.Request{Op: "t"})
	if err == nil {
		t.Fatal("want normalized error after panic")
	}
	if runs != 1 {
		t.Fatalf("finalizer ran %d times on panic, want exactly 1", runs)
	}
	if len(events) != 1 || events[0].Outcome != core.OutcomePanic {
		t.Fatalf("events = %+v", events)
	}
}
