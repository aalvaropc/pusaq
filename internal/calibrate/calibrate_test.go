package calibrate

import (
	"context"
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

	res := Sweep(scored, 0.99)
	if !res.Qualified {
		t.Fatal("no τ qualified; expected τ above the trap similarity")
	}
	// τ* must exclude the trap (>0.9502) while keeping eq1: the smallest
	// sweep step above the trap.
	if res.TauStar.Tau < 0.95 || res.TauStar.Tau > 0.96 {
		t.Fatalf("τ* = %g, want just above the trap at ~0.9502", res.TauStar.Tau)
	}
	if res.TauStar.Precision != 1.0 {
		t.Fatalf("precision at τ* = %g, want 1.0", res.TauStar.Precision)
	}
	// Only eq1 (sim 1.0) survives: recall 1/2.
	if res.TauStar.Recall != 0.5 {
		t.Fatalf("recall at τ* = %g, want 0.5", res.TauStar.Recall)
	}

	// At a low τ (0.80): everything predicted hit → precision 2/3, recall 1.
	first := res.Curve[0]
	if first.Tau != 0.80 || math.Abs(first.Precision-2.0/3.0) > 1e-9 || first.Recall != 1.0 {
		t.Fatalf("curve[0] = %+v, want τ=0.80 precision=2/3 recall=1", first)
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
	res := Sweep(scored, 0.99)
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
	res := Sweep(scored, 0.99)
	if res.Qualified {
		t.Fatalf("qualified at τ* = %+v with zero predicted positives", res.TauStar)
	}
}

func TestSweepReproducible(t *testing.T) {
	e := vecEmbedder{vecs: map[string][]float32{
		"a": angled(0), "b": angled(0.2),
	}}
	scored, _ := Score(context.Background(), e, []Pair{{A: "a", B: "b", Label: LabelEquivalent}})
	r1 := Sweep(scored, 0.95)
	r2 := Sweep(scored, 0.95)
	if len(r1.Curve) != len(r2.Curve) || r1.TauStar != r2.TauStar || r1.Qualified != r2.Qualified {
		t.Fatal("two sweeps over the same data differ")
	}
	for i := range r1.Curve {
		if r1.Curve[i] != r2.Curve[i] {
			t.Fatalf("curve diverges at %d", i)
		}
	}
}
