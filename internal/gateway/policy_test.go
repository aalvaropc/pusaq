package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/spend"
)

// fakeSpend scripts the budget counter.
type fakeSpend struct {
	month float64
	err   error
	adds  []float64
}

func (f *fakeSpend) Month(context.Context, string, time.Time) (float64, error) {
	return f.month, f.err
}

func (f *fakeSpend) Add(_ context.Context, _ string, _ time.Time, usd float64) (float64, error) {
	f.adds = append(f.adds, usd)
	f.month += usd
	return f.month, nil
}

func policyRoutes() map[string]Route {
	return map[string]Route{
		"pusaq/fast": {Provider: "mock", Model: "mock-model"},
		"pusaq/best": {Provider: "mock", Model: "mock-model"},
	}
}

// policyServer builds an authenticated pipeline (tenant team-support via
// sk-support) with the given options; admitter nil skips the RPM stage.
func policyServer(t *testing.T, p provider.Provider, admitter Admitter, opts CompletionsOptions) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	stages := []core.Stage{Accounting(emit, discardLogger()), RequestID(), Auth(authKeys())}
	if admitter != nil {
		stages = append(stages, RateLimit(admitter, opts.Policies, discardLogger()))
	}
	opts.Logger = discardLogger()
	completions := CompletionsHandler(policyRoutes(), map[string]provider.Provider{"mock": p}, opts)
	return NewServer(discardLogger(), core.Chain(stages...), completions), &events
}

func supportOnly(p Policy) PolicyFor {
	return func(tenant string) Policy {
		if tenant == "team-support" {
			return p
		}
		return Policy{}
	}
}

func postAs(s *Server, key, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	s.ServeHTTP(rec, req)
	return rec
}

const supportBody = `{"model":"pusaq/best","messages":[{"role":"user","content":"hello"}]}`

func TestPolicyDeniesDisallowedModel(t *testing.T) {
	p := okProvider()
	tokens := &fakeTokenLimiter{allowed: true}
	s, events := policyServer(t, p, nil, CompletionsOptions{
		Policies: supportOnly(Policy{Models: map[string]bool{"pusaq/fast": true}}),
		Tokens:   tokens, ReserveDefault: 10,
	})
	rec := postAs(s, "sk-support", supportBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s, want 403", rec.Code, rec.Body.String())
	}
	if p.got.Model != "" || len(tokens.charges) != 0 {
		t.Fatal("provider called or tokens charged for a denied request")
	}
	if rec.Header().Get(ServedModelHeader) != "mock-model" {
		t.Fatalf("%s missing on denial", ServedModelHeader)
	}
	e := (*events)[0]
	if e.ErrorCode != core.CodePolicyDenied || e.RequestedModel != "pusaq/best" {
		t.Fatalf("event = %+v", e)
	}
	// Another tenant is unrestricted.
	if rec := postAs(s, "sk-docs", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("unrestricted tenant status = %d", rec.Code)
	}
}

func TestPolicyDeniesAutoResolvedToDisallowedAlias(t *testing.T) {
	p := okProvider()
	s, _ := policyServer(t, p, nil, CompletionsOptions{
		Policies: supportOnly(Policy{Models: map[string]bool{"pusaq/fast": true}}),
		Rules:    RoutingRules{ByTag: map[string]string{"reason": "pusaq/best"}, Default: "pusaq/fast"},
	})
	rec := postAs(s, "sk-support", `{"model":"pusaq/auto","metadata":{"feature_tag":"reason"},"messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "pusaq/best") || !strings.Contains(rec.Body.String(), "pusaq/auto") {
		t.Fatalf("status = %d body=%s, want 403 naming both aliases", rec.Code, rec.Body.String())
	}
	if rec := postAs(s, "sk-support", `{"model":"pusaq/auto","messages":[{"role":"user","content":"x"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("untagged auto status = %d, want 200 via the default alias", rec.Code)
	}
}

func TestPolicyDeniesOversizedPrompt(t *testing.T) {
	p := okProvider()
	s, events := policyServer(t, p, nil, CompletionsOptions{Policies: supportOnly(Policy{MaxPromptChars: 4})})
	if rec := postAs(s, "sk-support", supportBody); rec.Code != http.StatusForbidden { // "hello" = 5
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if (*events)[0].ErrorCode != core.CodePolicyDenied {
		t.Fatalf("event = %+v", (*events)[0])
	}
	if rec := postAs(s, "sk-support", `{"model":"pusaq/best","messages":[{"role":"user","content":"hell"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("prompt at the cap status = %d, want 200", rec.Code)
	}
}

func TestPolicyBudgetExceeded(t *testing.T) {
	p := okProvider()
	sp := &fakeSpend{month: 50}
	s, events := policyServer(t, p, nil, CompletionsOptions{
		Policies: supportOnly(Policy{BudgetUSD: 50}), Spend: sp, Prices: testPrices(),
	})
	rec := postAs(s, "sk-support", supportBody)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d body=%s, want 402", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"retry_after_ms":`) || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("retry guidance missing: %s %v", rec.Body.String(), rec.Header())
	}
	if p.got.Model != "" || len(sp.adds) != 0 {
		t.Fatal("provider called or spend added on a denied request")
	}
	e := (*events)[0]
	if e.ErrorCode != core.CodeBudgetExceeded || e.SpendUSDMonth != 50 {
		t.Fatalf("event = %+v", e)
	}
}

func TestPolicyBudgetIncrementsSpend(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	counter := spend.New(client)

	p := okProvider()
	p.resp.Usage = core.Usage{PromptTokens: 12, CompletionTokens: 11} // 5.6e-6 USD at testPrices
	s, events := policyServer(t, p, nil, CompletionsOptions{
		Policies: supportOnly(Policy{BudgetUSD: 1}), Spend: counter, Prices: testPrices(),
	})
	if rec := postAs(s, "sk-support", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	month, err := counter.Month(context.Background(), "team-support", time.Now())
	if err != nil || month != 5.6e-6 {
		t.Fatalf("month = %v, %v, want 5.6e-06", month, err)
	}
	if e := (*events)[0]; e.SpendUSDMonth != 5.6e-6 {
		t.Fatalf("event spend = %v", e.SpendUSDMonth)
	}
	if ttl := mr.TTL(spend.Key("team-support", time.Now())); ttl <= 0 {
		t.Fatalf("ttl = %s", ttl)
	}

	// A streamed completion counts at stream end.
	p.sse = fakeSSE // usage 9/4 → 2.5e-6
	if rec := postAs(s, "sk-support", `{"model":"pusaq/best","messages":[{"role":"user","content":"x"}],"stream":true}`); rec.Code != http.StatusOK {
		t.Fatalf("stream status = %d", rec.Code)
	}
	month, _ = counter.Month(context.Background(), "team-support", time.Now())
	if month < 8.09e-6 || month > 8.11e-6 {
		t.Fatalf("month after stream = %v, want 8.1e-06", month)
	}
	if e := (*events)[1]; e.SpendUSDMonth < 8.09e-6 || e.SpendUSDMonth > 8.11e-6 {
		t.Fatalf("stream event spend = %v", e.SpendUSDMonth)
	}
}

func TestPolicyBudgetCountsEstimatedStream(t *testing.T) {
	sp := &fakeSpend{}
	p := okProvider()
	p.sse = "data: [DONE]\n\n"
	s, events := policyServer(t, p, nil, CompletionsOptions{
		Policies: supportOnly(Policy{BudgetUSD: 1}), Spend: sp, Prices: testPrices(), ReserveDefault: 512,
	})
	if rec := postAs(s, "sk-support", `{"model":"pusaq/best","messages":[{"role":"user","content":"hello"}],"stream":true}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	want := estimatedCost(t, 512).USD
	if len(sp.adds) != 1 || sp.adds[0] != want || (*events)[0].SpendUSDMonth != want {
		t.Fatalf("adds = %v, event spend = %v; want one estimate of %v", sp.adds, (*events)[0].SpendUSDMonth, want)
	}
}

func TestPolicyBudgetSkipsUnpricedAndFailed(t *testing.T) {
	sp := &fakeSpend{}
	failing := &fakeProvider{name: "mock", err: core.NewError(core.CodeUpstream, "upstream provider returned status 500")}
	s, _ := policyServer(t, failing, nil, CompletionsOptions{
		Policies: supportOnly(Policy{BudgetUSD: 1}), Spend: sp, Prices: testPrices(),
	})
	if rec := postAs(s, "sk-support", supportBody); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	// No price table: nothing to count either.
	s2, events := policyServer(t, okProvider(), nil, CompletionsOptions{Policies: supportOnly(Policy{BudgetUSD: 1}), Spend: sp})
	if rec := postAs(s2, "sk-support", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(sp.adds) != 0 || (*events)[0].SpendUSDMonth != 0 {
		t.Fatalf("spend added for unpriced or failed requests: %v", sp.adds)
	}
}

func TestPolicyBudgetNotCountedWithoutBudget(t *testing.T) {
	sp := &fakeSpend{}
	s, events := policyServer(t, okProvider(), nil, CompletionsOptions{Policies: supportOnly(Policy{}), Spend: sp, Prices: testPrices()})
	if rec := postAs(s, "sk-support", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(sp.adds) != 0 || (*events)[0].SpendUSDMonth != 0 {
		t.Fatal("spend counted for an unbudgeted tenant")
	}
}

func TestFailClosedRPM(t *testing.T) {
	admitter := &fakeAdmitter{err: errors.New("dial tcp: connection refused")}
	s, events := policyServer(t, okProvider(), admitter, CompletionsOptions{Policies: supportOnly(Policy{FailClosed: true})})
	if rec := postAs(s, "sk-support", supportBody); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed tenant status = %d, want 503", rec.Code)
	}
	if (*events)[0].ErrorCode != core.CodeStoreUnavailable {
		t.Fatalf("event = %+v", (*events)[0])
	}
	if rec := postAs(s, "sk-docs", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("fail-open tenant status = %d, want 200", rec.Code)
	}
}

func TestFailClosedTPM(t *testing.T) {
	tokens := &fakeTokenLimiter{allowErr: errors.New("dial tcp: connection refused")}
	s, _ := policyServer(t, okProvider(), nil, CompletionsOptions{Policies: supportOnly(Policy{FailClosed: true}), Tokens: tokens, ReserveDefault: 10})
	if rec := postAs(s, "sk-support", supportBody); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec := postAs(s, "sk-docs", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("fail-open tenant status = %d, want 200", rec.Code)
	}
}

func TestFailClosedBudgetRead(t *testing.T) {
	sp := &fakeSpend{err: errors.New("dial tcp: connection refused")}
	closed, _ := policyServer(t, okProvider(), nil, CompletionsOptions{Policies: supportOnly(Policy{BudgetUSD: 1, FailClosed: true}), Spend: sp, Prices: testPrices()})
	if rec := postAs(closed, "sk-support", supportBody); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	open, _ := policyServer(t, okProvider(), nil, CompletionsOptions{Policies: supportOnly(Policy{BudgetUSD: 1}), Spend: sp, Prices: testPrices()})
	if rec := postAs(open, "sk-support", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("fail-open status = %d, want 200", rec.Code)
	}
}
