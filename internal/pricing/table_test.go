package pricing

import (
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
)

func TestCostIsLinearInTokens(t *testing.T) {
	tb := NewTable("v1", map[string]Price{"m": {InputPerM: 1, OutputPerM: 3}})
	usd, ok := tb.Cost("m", core.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000})
	if !ok || usd != 4 {
		t.Fatalf("cost = %v ok=%v, want 4", usd, ok)
	}
}

func TestCostRoundsToNanoUSD(t *testing.T) {
	tb := NewTable("v1", map[string]Price{"m": {InputPerM: 0.10, OutputPerM: 0.40}})
	usd, ok := tb.Cost("m", core.Usage{PromptTokens: 12, CompletionTokens: 11})
	if !ok || usd != 5.6e-6 {
		t.Fatalf("cost = %v ok=%v, want exactly 5.6e-06", usd, ok)
	}
}

func TestUnknownModelIsNotPriced(t *testing.T) {
	tb := NewTable("v1", map[string]Price{"m": {InputPerM: 1, OutputPerM: 1}})
	if _, ok := tb.Cost("other", core.Usage{PromptTokens: 1}); ok {
		t.Fatal("unknown model was priced")
	}
	if tb.Has("other") || !tb.Has("m") || len(tb.Models()) != 1 {
		t.Fatalf("membership wrong: models=%v", tb.Models())
	}
}
