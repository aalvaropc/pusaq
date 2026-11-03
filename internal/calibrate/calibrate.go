// Package calibrate chooses the semantic cache's similarity threshold τ the
// only acceptable way (docs/design.md §5.4): the smallest τ whose precision
// lower confidence bound meets a floor — never by hit-rate. It runs on the
// same Embedder implementations the gateway uses, so calibrated numbers and
// production numbers share one code path.
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
	Tau            float64
	Hits           int
	FalseHits      int
	Precision      float64
	PrecisionLower float64
	Recall         float64
	F1             float64
}

// Result is a full calibration outcome.
type Result struct {
	Curve []Point
	// TauStar is the smallest τ whose precision lower bound meets the
	// floor; zero when none qualifies (Qualified false).
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
// selects τ* = min{τ : PrecisionLower(τ) ≥ floor}, PrecisionLower being the
// one-sided Clopper–Pearson bound at confidence. Qualified is false when no
// τ qualifies — the caller must fail loudly rather than pick a τ anyway.
func Sweep(scored []ScoredPair, floor, confidence float64) Result {
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
		p := Point{
			Tau:            tau,
			Hits:           tp + fp,
			FalseHits:      fp,
			Precision:      ratio(tp, tp+fp),
			PrecisionLower: lowerBound(tp, tp+fp, confidence),
			Recall:         ratio(tp, tp+fn),
			F1:             ratio(2*tp, 2*tp+fp+fn),
		}
		res.Curve = append(res.Curve, p)

		if !res.Qualified && p.PrecisionLower >= floor {
			res.TauStar = p
			res.Qualified = true
		}
	}
	return res
}

func ratio(num, den int) float64 {
	if den == 0 {
		return math.NaN()
	}
	return float64(num) / float64(den)
}

func lowerBound(successes, trials int, confidence float64) float64 {
	if trials == 0 {
		return math.NaN()
	}
	if successes == 0 {
		return 0
	}
	alpha := 1 - confidence
	lo, hi := 0.0, 1.0
	for range 64 {
		mid := (lo + hi) / 2
		if upperTail(successes, trials, mid) < alpha {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

func upperTail(successes, trials int, p float64) float64 {
	n := float64(trials)
	lgN, _ := math.Lgamma(n + 1)
	sum := 0.0
	for k := successes; k <= trials; k++ {
		lgK, _ := math.Lgamma(float64(k) + 1)
		lgRest, _ := math.Lgamma(n - float64(k) + 1)
		sum += math.Exp(lgN - lgK - lgRest + float64(k)*math.Log(p) + (n-float64(k))*math.Log1p(-p))
	}
	return sum
}

// MinHits is how many predicted hits, none false, first reach floor at confidence.
func MinHits(floor, confidence float64) int {
	return int(math.Ceil(math.Log(1-confidence) / math.Log(floor)))
}
