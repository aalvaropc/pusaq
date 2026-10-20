package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/pricing"
	"github.com/aalvaropc/pusaq/internal/provider"
)

func testPrices() *pricing.Table {
	return pricing.NewTable("v-test", map[string]pricing.Price{"mock-model": {InputPerM: 0.10, OutputPerM: 0.40}})
}

// pricedServer wires the handler with a price table and captures events.
func pricedServer(t *testing.T, p provider.Provider, prices *pricing.Table) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID())
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Prices: prices, ReserveDefault: 512, Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions), &events
}

func TestCompletionEventCarriesCost(t *testing.T) {
	p := okProvider()
	p.resp.Usage = core.Usage{PromptTokens: 12, CompletionTokens: 11}
	s, events := pricedServer(t, p, testPrices())

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(*events) != 1 {
		t.Fatalf("events = %d, want 1", len(*events))
	}
	want := core.Cost{USD: 5.6e-6, TableVersion: "v-test"}
	if (*events)[0].Cost != want {
		t.Fatalf("cost = %+v, want %+v", (*events)[0].Cost, want)
	}
}

func TestStreamedCompletionPricedAtStreamEnd(t *testing.T) {
	p := okProvider()
	p.sse = fakeSSE // carries usage 9/4 in its final chunk
	s, events := pricedServer(t, p, testPrices())

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(*events) != 1 {
		t.Fatalf("events = %d, want 1 at stream end", len(*events))
	}
	// 9 × 0.10 + 4 × 0.40 = 2.5 per million.
	want := core.Cost{USD: 2.5e-6, TableVersion: "v-test"}
	if (*events)[0].Cost != want {
		t.Fatalf("cost = %+v, want %+v", (*events)[0].Cost, want)
	}
}

func estimatedCost(t *testing.T, completionTokens int) core.Cost {
	t.Helper()
	usd, ok := testPrices().Cost("mock-model", core.Usage{PromptTokens: 3, CompletionTokens: completionTokens})
	if !ok {
		t.Fatal("mock-model is not priced")
	}
	return core.Cost{USD: usd, TableVersion: "v-test", Estimated: true}
}

func TestStreamWithoutUsageIsPricedFromReservation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		output  int
	}{
		{"max_tokens", `{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}],"max_tokens":100,"stream":true}`, 100},
		{"default reservation", string(streamingPayload()), 512},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := okProvider()
			p.sse = "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\ndata: [DONE]\n\n"
			s, events := pricedServer(t, p, testPrices())

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.payload)))

			e := (*events)[0]
			if e.Outcome != core.OutcomeSuccess || e.Usage != (core.Usage{}) {
				t.Fatalf("event = %+v, want success without provider usage", e)
			}
			if want := estimatedCost(t, tc.output); e.Cost != want {
				t.Fatalf("cost = %+v, want %+v", e.Cost, want)
			}
		})
	}
}

func TestInterruptedStreamIsPricedFromReservation(t *testing.T) {
	p := okProvider()
	p.sse = fakeSSE
	var events []core.CostEvent
	h := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, discardLogger()), RequestID())(
		CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
			CompletionsOptions{Prices: testPrices(), ReserveDefault: 512, Logger: discardLogger()}))

	resp, err := h(context.Background(), &core.Request{Op: "chat.completions", Payload: streamingPayload()})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Stream.Close()

	e := events[0]
	if e.ErrorCode != core.CodeStreamInterrupted {
		t.Fatalf("event = %+v, want %s", e, core.CodeStreamInterrupted)
	}
	if want := estimatedCost(t, 512); e.Cost != want {
		t.Fatalf("cost = %+v, want %+v", e.Cost, want)
	}
}

func TestUpstreamErrorEventHasNoCost(t *testing.T) {
	p := &fakeProvider{name: "mock", err: core.NewError(core.CodeUpstream, "upstream provider returned status 500")}
	s, events := pricedServer(t, p, testPrices())

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if got := (*events)[0].Cost; got != (core.Cost{}) {
		t.Fatalf("cost = %+v, want none on an unanswered request", got)
	}
}

func TestNoPriceTableLeavesCostAbsent(t *testing.T) {
	s, events := pricedServer(t, okProvider(), nil)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := (*events)[0].Cost; got != (core.Cost{}) {
		t.Fatalf("cost = %+v, want none without a table", got)
	}
}
