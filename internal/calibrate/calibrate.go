// Package calibrate chooses the semantic cache's similarity threshold τ the
// only acceptable way (docs/design.md §5.4): by sweeping a labeled pair
// dataset for the smallest τ whose precision meets a floor — never by
// hit-rate. It runs on the same Embedder implementations the gateway uses,
// so calibrated numbers and production numbers share one code path.
package calibrate

import (
	"context"
	"fmt"
	"math"

	"github.com/aalvaropc/pusaq/internal/embed"
)

// Label classifies a pair in the dataset.
type Label string

const (
	// LabelEquivalent marks two texts with the same intent — a cache hit
	// between them would be correct.
	LabelEquivalent Label = "equivalent"
	// LabelTrap marks two superficially similar texts with different
	// answers — a hit between them would be a served wrong answer.
	LabelTrap Label = "trap"
)

// Pair is one labeled example.
type Pair struct {
	A, B  string
	Label Label
}

// ScoredPair carries a pair's measured similarity.
type ScoredPair struct {
	Pair
	Similarity float64
}

// Point is one step of the precision/recall curve.
type Point struct {
	Tau       float64
	Precision float64
	Recall    float64
	F1        float64
}

// Result is a full calibration outcome.
type Result struct {
	Curve []Point
	// TauStar is the smallest τ whose precision meets the floor with
	// recall > 0; zero when none qualifies (Qualified false).
	TauStar   Point
	Qualified bool
	Embedder  string
}

// Sweep bounds: τ ∈ [0.80, 0.995] in steps of 0.005.
const (
	sweepLo   = 0.80
	sweepHi   = 0.995
	sweepStep = 0.005
)

// Score embeds every pair once and computes its similarity. Embedding
// happens once per side — the τ sweep afterwards is pure arithmetic, so
// API-embedder runs stay affordable.
func Score(ctx context.Context, e embed.Embedder, pairs []Pair) ([]ScoredPair, error) {
	scored := make([]ScoredPair, 0, len(pairs))
	for i, p := range pairs {
		va, err := e.Embed(ctx, p.A)
		if err != nil {
			return nil, fmt.Errorf("pair %d side A: %w", i, err)
		}
		vb, err := e.Embed(ctx, p.B)
		if err != nil {
			return nil, fmt.Errorf("pair %d side B: %w", i, err)
		}
		scored = append(scored, ScoredPair{Pair: p, Similarity: embed.Cosine(va, vb)})
	}
	return scored, nil
}

// Sweep computes the precision/recall curve over the scored pairs and
// selects τ* = min{τ : precision(τ) ≥ floor ∧ recall(τ) > 0}. A τ with no
// predicted positives has undefined precision; it never qualifies (it
// recommends nothing). Qualified is false when no τ qualifies — the caller
// must fail loudly rather than pick a τ anyway.
func Sweep(scored []ScoredPair, floor float64) Result {
	var res Result
	for tau := sweepLo; tau <= sweepHi+1e-9; tau += sweepStep {
		tau = math.Round(tau*1000) / 1000
		var tp, fp, fn int
		for _, s := range scored {
			predictedHit := s.Similarity >= tau
			switch {
			case predictedHit && s.Label == LabelEquivalent:
				tp++
			case predictedHit && s.Label == LabelTrap:
				fp++
			case !predictedHit && s.Label == LabelEquivalent:
				fn++
			}
		}
		p := Point{Tau: tau}
		if tp+fp > 0 {
			p.Precision = float64(tp) / float64(tp+fp)
		}
		if tp+fn > 0 {
			p.Recall = float64(tp) / float64(tp+fn)
		}
		if p.Precision+p.Recall > 0 {
			p.F1 = 2 * p.Precision * p.Recall / (p.Precision + p.Recall)
		}
		res.Curve = append(res.Curve, p)

		if !res.Qualified && tp+fp > 0 && p.Precision >= floor && p.Recall > 0 {
			res.TauStar = p
			res.Qualified = true
		}
	}
	return res
}
