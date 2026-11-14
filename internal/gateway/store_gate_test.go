package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
	"github.com/aalvaropc/pusaq/internal/spend"
	"github.com/aalvaropc/pusaq/internal/storegate"
)

type storeFault struct {
	mu    sync.Mutex
	calls map[string]int
	err   error
	hang  bool
}

func (f *storeFault) set(err error, hang bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err, f.hang = err, hang
}

func (f *storeFault) snapshot() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.calls)
}

func (f *storeFault) DialHook(next redis.DialHook) redis.DialHook { return next }

func (f *storeFault) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		f.mu.Lock()
		f.calls[cmd.Name()]++
		err, hang := f.err, f.hang
		f.mu.Unlock()
		if hang {
			<-ctx.Done()
			err = ctx.Err()
		}
		if err != nil {
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

func (f *storeFault) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		f.mu.Lock()
		for _, cmd := range cmds {
			f.calls[cmd.Name()]++
		}
		f.mu.Unlock()
		return next(ctx, cmds)
	}
}

// gatedStore is one store client, optionally behind a gate, with a fault
// injector between the gate and the store that counts what reaches it.
type gatedStore struct {
	gate   *storegate.Gate
	fault  *storeFault
	rpm    *ratelimit.Limiter
	tpm    *ratelimit.Limiter
	spend  *spend.Counter
	client *redis.Client
}

func newGatedStore(t *testing.T, gated bool) *gatedStore {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	s := &gatedStore{fault: &storeFault{calls: map[string]int{}}, client: client}
	if gated {
		s.gate = storegate.Attach(client, nil)
	}
	client.AddHook(s.fault)
	var err error
	if s.rpm, err = ratelimit.New(client, "rpm", ratelimit.Limits{Burst: 100, Rate: 1, TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if s.tpm, err = ratelimit.New(client, "tpm", ratelimit.Limits{Burst: 10_000, Rate: 100, TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	s.spend = spend.New(client)
	return s
}

func (s *gatedStore) server(t *testing.T, p provider.Provider, logger *slog.Logger) (*Server, *[]core.CostEvent) {
	t.Helper()
	policies := func(tenant string) Policy {
		switch tenant {
		case "team-support":
			return Policy{BudgetUSD: 100}
		case "team-docs":
			return Policy{FailClosed: true}
		}
		return Policy{}
	}
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	opts := CompletionsOptions{Policies: policies, Tokens: s.tpm, Spend: s.spend, Prices: testPrices(), ReserveDefault: 10, Logger: logger}
	pipeline := core.Chain(Accounting(emit, logger), RequestID(), Auth(authKeys()), RateLimit(s.rpm, policies, logger))
	return NewServer(logger, pipeline, CompletionsHandler(policyRoutes(), map[string]provider.Provider{"mock": p}, opts)), &events
}

func (s *gatedStore) open(t *testing.T) {
	t.Helper()
	s.fault.set(errors.New("dial tcp: connect: connection refused"), false)
	for range storegate.MinSamples {
		_, _ = s.rpm.Allow(context.Background(), "warm-up", 1)
	}
	if got := s.gate.State(); got != breaker.StateOpen {
		t.Fatalf("gate state = %s, want open", got)
	}
	s.fault.set(nil, true)
}

func TestStoreGateLeavesTheHealthyPathUnchanged(t *testing.T) {
	var seen [2]map[string]int
	for i, gated := range []bool{false, true} {
		s := newGatedStore(t, gated)
		srv, events := s.server(t, okProvider(), discardLogger())
		for range 5 {
			for _, key := range []string{"sk-support", "sk-docs"} {
				if rec := postAs(srv, key, supportBody); rec.Code != http.StatusOK {
					t.Fatalf("gated=%t %s status = %d", gated, key, rec.Code)
				}
			}
		}
		for _, e := range *events {
			if len(e.FailOpen) > 0 {
				t.Fatalf("gated=%t: healthy store failed open: %+v", gated, e)
			}
		}
		seen[i] = s.fault.snapshot()
	}
	if !maps.Equal(seen[0], seen[1]) || seen[1]["ping"] != 0 {
		t.Fatalf("store calls without the gate %v, with it %v", seen[0], seen[1])
	}
}

func TestOpenStoreGateDecidesAtOnce(t *testing.T) {
	s := newGatedStore(t, true)
	var logs bytes.Buffer
	srv, events := s.server(t, okProvider(), slog.New(slog.NewJSONHandler(&logs, nil)))
	s.open(t)
	before := s.fault.snapshot()

	start := time.Now()
	open := postAs(srv, "sk-support", supportBody)
	closed := postAs(srv, "sk-docs", supportBody)
	if elapsed := time.Since(start); elapsed >= limiterBudget {
		t.Fatalf("two requests took %s with the gate open, want no store wait", elapsed)
	}
	if open.Code != http.StatusOK || closed.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-open status = %d, fail-closed status = %d; want 200 and 503", open.Code, closed.Code)
	}
	if got := s.fault.snapshot(); !maps.Equal(got, before) {
		t.Fatalf("store calls went from %v to %v with the gate open", before, got)
	}
	if e := (*events)[0]; strings.Join(e.FailOpen, ",") != "rpm,budget,tpm" || e.Outcome != core.OutcomeSuccess {
		t.Fatalf("fail-open event = %+v, want rpm,budget,tpm", e)
	}
	if e := (*events)[1]; e.ErrorCode != core.CodeStoreUnavailable || len(e.FailOpen) != 0 {
		t.Fatalf("fail-closed event = %+v, want store_unavailable", e)
	}
	for _, msg := range []string{
		"rate limiter store unavailable, failing open",
		"spend store unavailable, failing open",
		"token limiter store unavailable, failing open",
		"spend counter update failed; spend under-counted",
		"rate limiter store unavailable, failing closed",
	} {
		if !strings.Contains(logs.String(), `"msg":"`+msg+`"`) {
			t.Fatalf("missing %q in logs: %s", msg, logs.String())
		}
	}
}

type openingProvider struct {
	*fakeProvider
	open func()
}

func (p openingProvider) Complete(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.open()
	return p.fakeProvider.Complete(ctx, req)
}

func TestOpenStoreGateSkipsReconciliationAndIncrement(t *testing.T) {
	s := newGatedStore(t, true)
	var logs bytes.Buffer
	var before map[string]int
	p := openingProvider{fakeProvider: okProvider(), open: func() {
		s.open(t)
		before = s.fault.snapshot()
	}}
	srv, events := s.server(t, p, slog.New(slog.NewJSONHandler(&logs, nil)))
	if rec := postAs(srv, "sk-support", supportBody); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := (*events)[0]; e.TokensCharged == 0 || len(e.FailOpen) != 0 {
		t.Fatalf("event = %+v, want tokens charged before the gate opened", e)
	}
	if got := s.fault.snapshot(); !maps.Equal(got, before) {
		t.Fatalf("finalizers reached the store with the gate open: %v → %v", before, got)
	}
	for _, msg := range []string{"token reconciliation failed; charge stands until bucket TTL", "spend counter update failed; spend under-counted"} {
		if !strings.Contains(logs.String(), `"msg":"`+msg+`"`) {
			t.Fatalf("missing %q in logs: %s", msg, logs.String())
		}
	}
	if err := s.tpm.Adjust(context.Background(), "team-support", 5); !errors.Is(err, storegate.ErrOpen) {
		t.Fatalf("adjust error = %v, want ErrOpen", err)
	}
	if _, err := s.spend.Add(context.Background(), "team-support", time.Now(), 1); !errors.Is(err, storegate.ErrOpen) {
		t.Fatalf("spend add error = %v, want ErrOpen", err)
	}
}
