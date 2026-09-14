// Package pricing turns provider-reported token usage into dollars against
// a versioned price table (docs/design.md §5.7). Prices are configuration:
// physical model names and their rates never appear in code, and the table
// version travels on every cost event so historical reports still add up
// after prices change.
package pricing

import (
	"math"
	"sort"

	"github.com/aalvaropc/pusaq/internal/core"
)

// Price is what one physical model costs, in USD per million tokens.
type Price struct {
	InputPerM  float64
	OutputPerM float64
}

// Table maps physical models to prices under one version label.
type Table struct {
	Version string
	prices  map[string]Price
}

// NewTable builds a table; the map is copied.
func NewTable(version string, prices map[string]Price) *Table {
	cp := make(map[string]Price, len(prices))
	for k, v := range prices {
		cp[k] = v
	}
	return &Table{Version: version, prices: cp}
}

// Models lists the priced models, sorted.
func (t *Table) Models() []string {
	out := make([]string, 0, len(t.prices))
	for m := range t.prices {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Has reports whether model is priced.
func (t *Table) Has(model string) bool {
	_, ok := t.prices[model]
	return ok
}

// Cost prices a usage: prompt tokens at the input rate plus completion
// tokens at the output rate, rounded to a nano-dollar so float noise never
// reaches the event. ok is false for an unpriced model.
func (t *Table) Cost(model string, u core.Usage) (usd float64, ok bool) {
	p, ok := t.prices[model]
	if !ok {
		return 0, false
	}
	raw := (float64(u.PromptTokens)*p.InputPerM + float64(u.CompletionTokens)*p.OutputPerM) / 1e6
	return math.Round(raw*1e9) / 1e9, true
}
