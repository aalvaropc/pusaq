package replay

import (
	"context"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/embed"
)

type fixedEmbedder struct {
	vectors map[string][]float32
	calls   map[string]int
}

func (fixedEmbedder) Name() string { return "fixed" }

func (f fixedEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v, ok := f.vectors[text]
	if !ok {
		return nil, fmt.Errorf("unexpected embedding of %q", text)
	}
	f.calls[text]++
	return embed.Normalize(slices.Clone(v)), nil
}

func handBuilt(t *testing.T) (*Corpus, fixedEmbedder, []Request) {
	t.Helper()
	c, err := NewCorpus([]Prompt{
		{Intent: "a", Lang: "en", Family: "fa", Kind: "entity", Text: "a1"},
		{Intent: "a", Lang: "en", Family: "fa", Kind: "entity", Text: "a2"},
		{Intent: "b", Lang: "en", Family: "fa", Kind: "entity", Text: "b1"},
		{Intent: "a", Lang: "es", Family: "fa", Kind: "entity", Text: "ae"},
		{Intent: "c", Lang: "en", Family: "fc", Kind: "none", Text: "c1"},
		{Intent: "d", Lang: "en", Family: "fd", Kind: "none", Text: "rate today"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := fixedEmbedder{calls: map[string]int{}, vectors: map[string][]float32{
		"a1": {1, 0, 0},
		"a2": {0.96, 0.28, 0},
		"b1": {0.98, 0, float32(math.Sqrt(1 - 0.98*0.98))},
		"ae": {0.97, 0, -float32(math.Sqrt(1 - 0.97*0.97))},
		"c1": {0, 0, 1},
	}}
	var reqs []Request
	for i, p := range []int{0, 1, 2, 0, 4, 5, 3, 0} {
		reqs = append(reqs, Request{Prompt: p, Provider: time.Duration(i+1) * 10 * time.Millisecond})
	}
	return c, e, reqs
}

var handBuiltConfig = Config{Prices: Prices{Input: 1, Output: 2, Embed: 0.5}, OutputTokens: 10, IndexEntries: 100, IndexBytes: 1 << 20}

func runHandBuilt(t *testing.T) (*Replay, fixedEmbedder) {
	t.Helper()
	c, e, reqs := handBuilt(t)
	r, err := Run(e, c, reqs, handBuiltConfig)
	if err != nil {
		t.Fatal(err)
	}
	return r, e
}

func TestRunRecordsNeighborsInRequestOrder(t *testing.T) {
	r, e := runHandBuilt(t)
	want := []struct {
		neighbor int
		sim      float64
		correct  bool
		repeat   bool
		skip     string
	}{
		{-1, 0, false, false, ""},
		{0, 0.96, true, false, ""},
		{0, 0.98, false, false, ""},
		{0, 1, true, true, ""},
		{2, math.Sqrt(1 - 0.98*0.98), false, false, ""},
		{-1, 0, false, false, "temporal"},
		{0, 0.97, false, false, ""},
		{0, 1, true, true, ""},
	}
	for i, w := range want {
		o := r.Outcomes[i]
		if o.Neighbor != w.neighbor || math.Abs(o.Similarity-w.sim) > 1e-6 || o.Correct != w.correct || o.Repeat != w.repeat || o.Skip != w.skip {
			t.Errorf("request %d = neighbor %d sim %.6f correct %v repeat %v skip %q, want %+v", i, o.Neighbor, o.Similarity, o.Correct, o.Repeat, o.Skip, w)
		}
		if w.skip == "" && (!o.Inserted || o.Lookup <= 0) {
			t.Errorf("request %d: inserted %v lookup %s, want an insertion and a measured lookup", i, o.Inserted, o.Lookup)
		}
	}
	if e.calls["rate today"] != 0 {
		t.Fatal("an ineligible prompt was embedded")
	}
	if skipped := r.Outcomes[5]; skipped.EmbedUSD != 0 || skipped.ProviderUSD != 24e-6 || skipped.Lookup != 0 {
		t.Fatalf("ineligible request embed %g provider %g lookup %s, want no toll, its provider cost and no lookup", skipped.EmbedUSD, skipped.ProviderUSD, skipped.Lookup)
	}
	if r.Embedder != "fixed" {
		t.Fatalf("embedder = %q", r.Embedder)
	}
}

func TestHitAndFalseHitAccountingPerTau(t *testing.T) {
	r, _ := runHandBuilt(t)
	tests := []struct {
		tau                                      float64
		hits, falseHits, novelHits, novelCorrect int
		hitRate, fpRate, correctHit              float64
	}{
		{0.1, 6, 3, 4, 1, 6.0 / 7, 3.0 / 6, 3.0 / 7},
		{0.95, 5, 2, 3, 1, 5.0 / 7, 2.0 / 5, 3.0 / 7},
		{0.975, 3, 1, 1, 0, 3.0 / 7, 1.0 / 3, 2.0 / 7},
		{0.99, 2, 0, 0, 0, 2.0 / 7, 0, 2.0 / 7},
	}
	for _, tt := range tests {
		p := r.At(tt.tau)
		if p.Requests != 8 || p.Eligible != 7 || p.Hits != tt.hits || p.FalseHits != tt.falseHits || p.NovelHits != tt.novelHits || p.NovelCorrectHits() != tt.novelCorrect {
			t.Errorf("τ=%g: requests %d eligible %d hits %d false %d novel %d novel correct %d, want 8 7 %d %d %d %d", tt.tau, p.Requests, p.Eligible, p.Hits, p.FalseHits, p.NovelHits, p.NovelCorrectHits(), tt.hits, tt.falseHits, tt.novelHits, tt.novelCorrect)
		}
		if math.Abs(p.HitRate()-tt.hitRate) > 1e-12 || math.Abs(p.FPRate()-tt.fpRate) > 1e-12 || math.Abs(p.CorrectHitRate()-tt.correctHit) > 1e-12 {
			t.Errorf("τ=%g: hit-rate %g FP rate %g correct-hit rate %g, want %g %g %g", tt.tau, p.HitRate(), p.FPRate(), p.CorrectHitRate(), tt.hitRate, tt.fpRate, tt.correctHit)
		}
	}
	if sweep := r.Sweep([]float64{0.1, 0.99}); len(sweep) != 2 || sweep[1].Hits != 2 {
		t.Fatalf("sweep = %+v", sweep)
	}
	if p := (&Replay{}).At(0.9); !math.IsNaN(p.HitRate()) || !math.IsNaN(p.FPRate()) {
		t.Fatalf("empty replay rates = %g %g, want undefined", p.HitRate(), p.FPRate())
	}
}

func TestSavingsCountOnlyCorrectHitsNetOfTheToll(t *testing.T) {
	r, _ := runHandBuilt(t)
	p := r.At(0.95)
	const perRequest, toll = 22e-6, 0.5e-6
	if p.AvoidedUSD != 3*perRequest || p.WrongHitUSD != 2*perRequest || p.TollUSD != 7*toll {
		t.Fatalf("avoided %g wrong %g toll %g, want %g %g %g", p.AvoidedUSD, p.WrongHitUSD, p.TollUSD, 3*perRequest, 2*perRequest, 7*toll)
	}
	if p.NetUSD() != 62.5e-6 {
		t.Fatalf("net = %g, want 62.5e-6", p.NetUSD())
	}
	b := r.Baseline()
	if b.ProviderUSD != 178e-6 || b.EligibleUSD != 154e-6 || b.TollUSD != 3.5e-6 {
		t.Fatalf("baseline provider %g eligible %g toll %g, want 178e-6 154e-6 3.5e-6", b.ProviderUSD, b.EligibleUSD, b.TollUSD)
	}
	if math.Abs(b.BreakEven()-3.5/154) > 1e-12 {
		t.Fatalf("break-even = %g, want %g", b.BreakEven(), 3.5/154)
	}
	if b.Requests != 8 || b.Eligible != 7 || b.Repeats != 2 || b.NotInserted != 0 || b.Skips["temporal"] != 1 || len(b.Skips) != 1 {
		t.Fatalf("baseline counts = %+v", b)
	}
	if b.P50 != 40*time.Millisecond || b.P95 != 80*time.Millisecond {
		t.Fatalf("no-cache p50 %s p95 %s, want 40ms 80ms", b.P50, b.P95)
	}
	if !math.IsNaN((Baseline{}).BreakEven()) {
		t.Fatal("break-even without eligible spend is defined")
	}
}

func TestConfusionsClassifyFalseHits(t *testing.T) {
	r, _ := runHandBuilt(t)
	got := r.Confusions(0.1)
	want := []Confusion{
		{Request: "a/es", Neighbor: "a/en", Category: "language", Count: 1},
		{Request: "b/en", Neighbor: "a/en", Category: "trap", Kind: "entity", Count: 1},
		{Request: "c/en", Neighbor: "b/en", Category: "unrelated", Count: 1},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("confusions = %+v, want %+v", got, want)
	}
	if got := r.Confusions(0.99); len(got) != 0 {
		t.Fatalf("confusions at 0.99 = %+v, want none", got)
	}
}

func TestLatencyWithAndWithoutTheCache(t *testing.T) {
	ms := time.Millisecond
	r := &Replay{Outcomes: []Outcome{
		{Request: Request{Provider: 100 * ms}, Skip: "temporal", Neighbor: -1},
		{Request: Request{Provider: 200 * ms}, Neighbor: -1, Lookup: 1 * ms, Inserted: true},
		{Request: Request{Provider: 300 * ms}, Neighbor: 1, Similarity: 0.99, Correct: true, Lookup: 2 * ms, Inserted: true},
		{Request: Request{Provider: 400 * ms}, Neighbor: 1, Similarity: 0.5, Lookup: 3 * ms, Inserted: true},
	}}
	b := r.Baseline()
	if b.P50 != 200*ms || b.P95 != 400*ms {
		t.Fatalf("without cache p50 %s p95 %s, want 200ms 400ms", b.P50, b.P95)
	}
	if b.LookupP50 != 2*ms || b.LookupP95 != 3*ms {
		t.Fatalf("lookup p50 %s p95 %s, want 2ms 3ms", b.LookupP50, b.LookupP95)
	}
	p := r.At(0.95)
	if p.P50 != 100*ms || p.P95 != 403*ms {
		t.Fatalf("with cache p50 %s p95 %s, want 100ms 403ms", p.P50, p.P95)
	}
}

func TestPercentilesUseNearestRank(t *testing.T) {
	sample := make([]int64, 0, 100)
	for v := int64(100); v >= 1; v-- {
		sample = append(sample, v)
	}
	p50, p95 := percentiles(sample)
	if p50 != 50 || p95 != 95 {
		t.Fatalf("p50 %d p95 %d, want 50 95", p50, p95)
	}
	if p50, p95 := percentiles([]int64{7}); p50 != 7 || p95 != 7 {
		t.Fatalf("single sample p50 %d p95 %d, want 7 7", p50, p95)
	}
}

func TestRunFailsLoudly(t *testing.T) {
	c, e, reqs := handBuilt(t)
	if _, err := Run(e, c, append(reqs, Request{Prompt: len(c.Prompts)}), handBuiltConfig); err == nil {
		t.Fatal("a request outside the corpus was accepted")
	}
	delete(e.vectors, "c1")
	if _, err := Run(e, c, reqs, handBuiltConfig); err == nil {
		t.Fatal("an embedder failure was not reported")
	}
	for _, cfg := range []Config{{IndexEntries: 0, IndexBytes: 1}, {IndexEntries: 1, IndexBytes: 0}, {IndexEntries: 1, IndexBytes: 1, OutputTokens: -1}} {
		if _, err := Run(e, c, reqs, cfg); err == nil {
			t.Errorf("config %+v accepted", cfg)
		}
	}
}

func TestReplayOfTheShippedCorpusIsReproducible(t *testing.T) {
	c := loadCorpus(t)
	o := trafficOptions(11)
	o.Requests = 400
	cfg := Config{Prices: Prices{Input: 0.1, Output: 0.4, Embed: 0.02}, OutputTokens: 11, IndexEntries: 10_000, IndexBytes: 64 << 20}
	run := func() *Replay {
		reqs, err := Traffic(c, o)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Run(embed.Local{}, c, reqs, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := run(), run()
	for i := range a.Outcomes {
		x, y := a.Outcomes[i], b.Outcomes[i]
		x.Lookup, y.Lookup = 0, 0
		if x != y {
			t.Fatalf("request %d differs between runs: %+v vs %+v", i, x, y)
		}
	}
	for _, tau := range []float64{0.8, 0.95} {
		x, y := a.At(tau), b.At(tau)
		x.P50, x.P95, y.P50, y.P95 = 0, 0, 0, 0
		if x != y {
			t.Fatalf("τ=%g differs between runs: %+v vs %+v", tau, x, y)
		}
	}
	if skips := a.Baseline().Skips; skips["temporal"] == 0 && slices.ContainsFunc(a.Outcomes, func(o Outcome) bool { return c.Prompts[o.Prompt].Intent == "exchange-rate" }) {
		t.Fatal("the time-dependent intent reached the cache")
	}
}
