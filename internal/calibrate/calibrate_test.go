package calibrate

import (
	"context"
	"fmt"
	"math"
	"testing"
)

// vecEmbedder maps known texts to fixed vectors so pair similarities are
// exact by construction.
type vecEmbedder struct{ vecs map[string][]float32 }

func (vecEmbedder) Name() string { return "fake" }

func (v vecEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return v.vecs[text], nil
}

// angled returns a unit vector at the given angle in the plane, so cosine
// similarity between two of them is cos(Δangle) exactly.
func angled(rad float64) []float32 {
	return []float32{float32(math.Cos(rad)), float32(math.Sin(rad))}
}

func TestSweepHandComputable(t *testing.T) {
	// Three pairs with exact similarities:
	//   eq1: cos(0)      = 1.00  (equivalent)
	//   eq2: cos(0.451)  ≈ 0.90  (equivalent)
	//   trap: cos(0.317) ≈ 0.95  (trap!) — sits between the equivalents.
	e := vecEmbedder{vecs: map[string][]float32{
		"a1": angled(0), "b1": angled(0),
		"a2": angled(0), "b2": angled(0.451),
		"a3": angled(0), "b3": angled(0.317),
	}}
	pairs := []Pair{
		{A: "a1", B: "b1", Label: LabelEquivalent},
		{A: "a2", B: "b2", Label: LabelEquivalent},
		{A: "a3", B: "b3", Label: LabelTrap},
	}
	scored, err := Score(context.Background(), e, pairs)
	if err != nil {
		t.Fatal(err)
	}

	res := Sweep(scored, 0.99, 0.95)
	if res.Qualified {
		t.Fatalf("qualified τ* = %+v on a single predicted hit", res.TauStar)
	}
	// Just above the trap (~0.9502) only eq1 (sim 1.0) is a hit: precision
	// 1 from one hit, whose 95% lower bound is 0.05^(1/1).
	above := pointAt(t, res, 0.955)
	if above.Hits != 1 || above.FalseHits != 0 || above.Precision != 1 || above.Recall != 0.5 ||
		math.Abs(above.PrecisionLower-0.05) > 1e-9 || math.Abs(above.F1-2.0/3.0) > 1e-9 {
		t.Fatalf("point above the trap = %+v", above)
	}
	// At a low τ (0.80): everything predicted hit → precision 2/3, recall 1.
	first := res.Curve[0]
	if first.Tau != 0.80 || first.Hits != 3 || first.FalseHits != 1 ||
		math.Abs(first.Precision-2.0/3.0) > 1e-9 || first.Recall != 1.0 || math.Abs(first.F1-0.8) > 1e-9 {
		t.Fatalf("curve[0] = %+v, want τ=0.80 precision=2/3 recall=1 F1=0.8", first)
	}
}

func pointAt(t *testing.T, res Result, tau float64) Point {
	t.Helper()
	for _, p := range res.Curve {
		if math.Abs(p.Tau-tau) < 1e-9 {
			return p
		}
	}
	t.Fatalf("no curve point at τ=%g", tau)
	return Point{}
}

func supportPairs(hits, falseHits int) []ScoredPair {
	var scored []ScoredPair
	add := func(n int, label Label, sim float64) {
		for range n {
			scored = append(scored, ScoredPair{Pair: Pair{Label: label}, Similarity: sim})
		}
	}
	add(50, LabelTrap, 0.85)
	add(50, LabelEquivalent, 0.70)
	add(hits, LabelEquivalent, 0.99)
	add(falseHits, LabelTrap, 0.99)
	return scored
}

func TestSweepRequiresSupport(t *testing.T) {
	tests := []struct {
		name            string
		hits, falseHits int
		want            bool
	}{
		{"one predicted hit", 1, 0, false},
		{"298 hits, none false", 298, 0, false},
		{"299 hits, none false", 299, 0, true},
		{"299 hits, one false", 298, 1, false},
		{"500 hits, one false", 499, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Sweep(supportPairs(tt.hits, tt.falseHits), 0.99, 0.95)
			above := pointAt(t, res, 0.855)
			if above.Hits != tt.hits+tt.falseHits || above.FalseHits != tt.falseHits {
				t.Fatalf("point above the traps = %+v", above)
			}
			if res.Qualified != tt.want {
				t.Fatalf("qualified = %t with lower bound %.5f from %d hits, want %t", res.Qualified, above.PrecisionLower, above.Hits, tt.want)
			}
			if tt.want && (res.TauStar.Tau != 0.855 || res.TauStar.PrecisionLower < 0.99) {
				t.Fatalf("τ* = %+v, want 0.855 with lower bound ≥ 0.99", res.TauStar)
			}
		})
	}
}

func TestLowerBound(t *testing.T) {
	const alpha = 0.05
	tests := []struct {
		name              string
		successes, trials int
		want              float64
	}{
		{"one of one", 1, 1, alpha},
		{"ten of ten", 10, 10, math.Pow(alpha, 1.0/10)},
		{"299 of 299", 299, 299, math.Pow(alpha, 1.0/299)},
		{"one of five", 1, 5, 1 - math.Pow(1-alpha, 1.0/5)},
		{"one of fifty", 1, 50, 1 - math.Pow(1-alpha, 1.0/50)},
		{"none of ten", 0, 10, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lowerBound(tt.successes, tt.trials, 1-alpha)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("lowerBound(%d, %d) = %.12f, want %.12f", tt.successes, tt.trials, got, tt.want)
			}
		})
	}
	if got := lowerBound(0, 0, 0.95); !math.IsNaN(got) {
		t.Fatalf("lowerBound without trials = %g, want NaN", got)
	}
	if got := lowerBound(499, 500, 0.95); got >= 499.0/500 {
		t.Fatalf("lowerBound(499, 500) = %g, want below the point estimate", got)
	}
}

func TestMinHits(t *testing.T) {
	for _, tt := range []struct {
		floor, confidence float64
		want              int
	}{
		{0.99, 0.95, 299},
		{0.9, 0.95, 29},
		{0.99, 0.99, 459},
	} {
		got := MinHits(tt.floor, tt.confidence)
		if got != tt.want {
			t.Fatalf("MinHits(%g, %g) = %d, want %d", tt.floor, tt.confidence, got, tt.want)
		}
		if lowerBound(got, got, tt.confidence) < tt.floor || lowerBound(got-1, got-1, tt.confidence) >= tt.floor {
			t.Fatalf("MinHits(%g, %g) = %d is not where the bound first reaches the floor", tt.floor, tt.confidence, got)
		}
	}
}

func TestSweepFailsWhenNoTauQualifies(t *testing.T) {
	// The trap is MORE similar than the equivalent: no τ can reach the
	// floor with recall > 0.
	e := vecEmbedder{vecs: map[string][]float32{
		"a1": angled(0), "b1": angled(0.451), // equivalent at 0.90
		"a2": angled(0), "b2": angled(0.2), // trap at 0.98
	}}
	scored, err := Score(context.Background(), e, []Pair{
		{A: "a1", B: "b1", Label: LabelEquivalent},
		{A: "a2", B: "b2", Label: LabelTrap},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := Sweep(scored, 0.99, 0.95)
	if res.Qualified {
		t.Fatalf("qualified with τ* = %+v despite an inseparable trap", res.TauStar)
	}
}

func TestSweepUndefinedPrecisionNeverQualifies(t *testing.T) {
	// One equivalent pair at 0.5: every τ in the sweep predicts nothing
	// (no positives) → undefined precision must not qualify.
	e := vecEmbedder{vecs: map[string][]float32{
		"a": angled(0), "b": angled(math.Pi / 3), // cos(60°) = 0.5
	}}
	scored, _ := Score(context.Background(), e, []Pair{{A: "a", B: "b", Label: LabelEquivalent}})
	res := Sweep(scored, 0.99, 0.95)
	if res.Qualified {
		t.Fatalf("qualified at τ* = %+v with zero predicted positives", res.TauStar)
	}
	for _, p := range res.Curve {
		if p.Hits != 0 || !math.IsNaN(p.Precision) || !math.IsNaN(p.PrecisionLower) {
			t.Fatalf("point %+v, want no hit and undefined precision, not 0", p)
		}
	}
}

func TestSweepReproducible(t *testing.T) {
	e := vecEmbedder{vecs: map[string][]float32{
		"a": angled(0), "b": angled(0.2),
	}}
	scored, _ := Score(context.Background(), e, []Pair{{A: "a", B: "b", Label: LabelEquivalent}})
	r1 := Sweep(scored, 0.95, 0.95)
	r2 := Sweep(scored, 0.95, 0.95)
	if fmt.Sprintf("%+v", r1) != fmt.Sprintf("%+v", r2) {
		t.Fatal("two sweeps over the same data differ")
	}
}
