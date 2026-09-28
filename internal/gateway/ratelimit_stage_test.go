package gateway

import (
	"context"
	"encoding/json"
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

// fakeAdmitter scripts the limiter's verdicts.
type fakeAdmitter struct {
	decision   ratelimit.Decision
	err        error
	sawTenants []string
}

func (f *fakeAdmitter) Allow(_ context.Context, tenant string, _ float64) (ratelimit.Decision, error) {
	f.sawTenants = append(f.sawTenants, tenant)
	return f.decision, f.err
}

func limitedServer(t *testing.T, admitter Admitter) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(
		Accounting(emit, discardLogger()),
		RequestID(),
		Auth(nil), // dev mode
		RateLimit(admitter, nil, discardLogger()),
	)
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": okProvider()}, CompletionsOptions{Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions), &events
}

func TestRateLimitDenialContract(t *testing.T) {
	admitter := &fakeAdmitter{decision: ratelimit.Decision{Allowed: false, RetryAfter: 1500 * time.Millisecond}}
	s, events := limitedServer(t, admitter)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want ceiled seconds \"2\"", got)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not normalized: %v", err)
	}
	if body.Error.Code != core.CodeRateLimited || body.Error.RetryAfterMS != 1500 {
		t.Fatalf("body = %+v", body)
	}
	if len(*events) != 1 || (*events)[0].ErrorCode != core.CodeRateLimited {
		t.Fatalf("events = %+v, want one rate_limited rejection", *events)
	}
	if admitter.sawTenants[0] != DevTenant {
		t.Fatalf("limiter keyed by %q, want the tenant", admitter.sawTenants[0])
	}
}

func TestRateLimitAllowsWithinBudget(t *testing.T) {
	admitter := &fakeAdmitter{decision: ratelimit.Decision{Allowed: true}}
	s, events := limitedServer(t, admitter)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(*events) != 1 || (*events)[0].Outcome != core.OutcomeSuccess {
		t.Fatalf("events = %+v", *events)
	}
}

func TestRateLimitFailsOpenOnStoreError(t *testing.T) {
	admitter := &fakeAdmitter{err: errors.New("dial tcp: connection refused")}
	s, events := limitedServer(t, admitter)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail-open)", rec.Code)
	}
	if len(*events) != 1 || (*events)[0].Outcome != core.OutcomeSuccess {
		t.Fatalf("events = %+v", *events)
	}
}

// slowAdmitter never answers within the stage's budget.
type slowAdmitter struct{}

func (slowAdmitter) Allow(ctx context.Context, _ string, _ float64) (ratelimit.Decision, error) {
	<-ctx.Done()
	return ratelimit.Decision{}, ctx.Err()
}

func TestRateLimitFailsOpenWithinBudgetOnSlowStore(t *testing.T) {
	s, _ := limitedServer(t, slowAdmitter{})

	start := time.Now()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail-open)", rec.Code)
	}
	if elapsed > limiterBudget+500*time.Millisecond {
		t.Fatalf("fail-open took %s, want bounded by the %s budget", elapsed, limiterBudget)
	}
}
