package gateway

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
	"github.com/aalvaropc/pusaq/internal/spend"

	"github.com/redis/go-redis/v9"
)

func blackholeStore(t *testing.T) *redis.Client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, c) }()
		}
	}()
	client := redis.NewClient(&redis.Options{Addr: ln.Addr().String(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestHungStoreFailsOpenWithinBudget(t *testing.T) {
	client := blackholeStore(t)
	rpm, err := ratelimit.New(client, "rpm", ratelimit.Limits{Burst: 60, Rate: 1, TTL: time.Minute})
	if err != nil {
		t.Fatalf("rpm: %v", err)
	}
	tpm, err := ratelimit.New(client, "tpm", ratelimit.Limits{Burst: 100000, Rate: 1000, TTL: time.Minute})
	if err != nil {
		t.Fatalf("tpm: %v", err)
	}
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	budget := func(string) Policy { return Policy{BudgetUSD: 10} }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), Auth(nil), RateLimit(rpm, budget, discardLogger()))
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": okProvider()}, CompletionsOptions{
		Tokens: tpm, ReserveDefault: 512, Prices: testPrices(), Spend: spend.New(client), Policies: budget, Logger: discardLogger(),
	})
	s := NewServer(discardLogger(), pipeline, completions)

	start := time.Now()
	rec := post(s, string(validPayload()))
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail open)", rec.Code)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("request took %v with a store that never answers, want every store call bounded by the limiter budget", elapsed)
	}
	if len(events) != 1 || events[0].Outcome != core.OutcomeSuccess {
		t.Fatalf("events = %+v, want one success", events)
	}
}

func TestStalledStreamIsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	backend := provider.NewHTTP("mock", upstream.URL, 100*time.Millisecond, discardLogger())
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), Auth(nil))
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": backend}, CompletionsOptions{Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, completions)

	done := make(chan struct{})
	go func() {
		post(s, string(streamingPayload()))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled stream never ended")
	}
	if len(events) != 1 || events[0].Outcome != core.OutcomeError || events[0].ErrorCode != core.CodeUpstream {
		t.Fatalf("events = %+v, want one %s", events, core.CodeUpstream)
	}
}
