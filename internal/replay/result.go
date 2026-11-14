package replay

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/aalvaropc/pusaq/internal/report"
)

// Point is the replay evaluated at one τ.
type Point struct {
	Tau         float64
	Requests    int
	Eligible    int
	Hits        int
	FalseHits   int
	NovelHits   int
	NovelFalse  int
	AvoidedUSD  float64
	WrongHitUSD float64
	TollUSD     float64
	P50         time.Duration
	P95         time.Duration
}

// HitRate is predicted hits over eligible requests.
func (p Point) HitRate() float64 { return ratio(p.Hits, p.Eligible) }

// FPRate is false hits over predicted hits.
func (p Point) FPRate() float64 { return ratio(p.FalseHits, p.Hits) }

// CorrectHits counts predicted hits whose neighbor carries the request's answer.
func (p Point) CorrectHits() int { return p.Hits - p.FalseHits }

// CorrectHitRate is correct hits over eligible requests.
func (p Point) CorrectHitRate() float64 { return ratio(p.CorrectHits(), p.Eligible) }

// NovelCorrectHits counts correct hits on text no earlier eligible request carried.
func (p Point) NovelCorrectHits() int { return p.NovelHits - p.NovelFalse }

// NetUSD is the provider cost avoided by correct hits minus the embedding toll.
func (p Point) NetUSD() float64 { return usd(p.AvoidedUSD - p.TollUSD) }

// At evaluates the replay at tau.
func (r *Replay) At(tau float64) Point {
	p := Point{Tau: tau, Requests: len(r.Outcomes)}
	latency := make([]int64, 0, len(r.Outcomes))
	for _, o := range r.Outcomes {
		if o.Skip != "" {
			latency = append(latency, int64(o.Provider))
			continue
		}
		p.Eligible++
		p.TollUSD += o.EmbedUSD
		if !o.Hit(tau) {
			latency = append(latency, int64(o.Lookup+o.Provider))
			continue
		}
		latency = append(latency, int64(o.Lookup))
		p.Hits++
		if !o.Repeat {
			p.NovelHits++
		}
		if o.Correct {
			p.AvoidedUSD += o.ProviderUSD
			continue
		}
		p.FalseHits++
		p.WrongHitUSD += o.ProviderUSD
		if !o.Repeat {
			p.NovelFalse++
		}
	}
	p.AvoidedUSD, p.WrongHitUSD, p.TollUSD = usd(p.AvoidedUSD), usd(p.WrongHitUSD), usd(p.TollUSD)
	p.P50, p.P95 = percentiles(latency)
	return p
}

// Sweep evaluates the replay at every tau.
func (r *Replay) Sweep(taus []float64) []Point {
	points := make([]Point, 0, len(taus))
	for _, tau := range taus {
		points = append(points, r.At(tau))
	}
	return points
}

// Baseline is the replayed traffic without a cache, with the cache's τ-independent costs.
type Baseline struct {
	Requests    int
	Eligible    int
	Repeats     int
	NotInserted int
	Skips       map[string]int
	ProviderUSD float64
	EligibleUSD float64
	TollUSD     float64
	P50         time.Duration
	P95         time.Duration
	LookupP50   time.Duration
	LookupP95   time.Duration
}

// BreakEven is the correct-hit rate over eligible requests at which net savings reach zero, when hits cost what the average eligible request costs.
func (b Baseline) BreakEven() float64 {
	if b.EligibleUSD == 0 {
		return math.NaN()
	}
	return b.TollUSD / b.EligibleUSD
}

// Baseline summarizes the replay without a cache.
func (r *Replay) Baseline() Baseline {
	b := Baseline{Requests: len(r.Outcomes), Skips: map[string]int{}}
	provider := make([]int64, 0, len(r.Outcomes))
	lookup := make([]int64, 0, len(r.Outcomes))
	for _, o := range r.Outcomes {
		provider = append(provider, int64(o.Provider))
		b.ProviderUSD += o.ProviderUSD
		if o.Skip != "" {
			b.Skips[o.Skip]++
			continue
		}
		b.Eligible++
		b.EligibleUSD += o.ProviderUSD
		b.TollUSD += o.EmbedUSD
		lookup = append(lookup, int64(o.Lookup))
		if o.Repeat {
			b.Repeats++
		}
		if !o.Inserted {
			b.NotInserted++
		}
	}
	b.ProviderUSD, b.EligibleUSD, b.TollUSD = usd(b.ProviderUSD), usd(b.EligibleUSD), usd(b.TollUSD)
	b.P50, b.P95 = percentiles(provider)
	b.LookupP50, b.LookupP95 = percentiles(lookup)
	return b
}

// Confusion counts false hits of one answer on another's cached entry.
type Confusion struct {
	Request  string
	Neighbor string
	Category string
	Kind     string
	Count    int
}

// Confusions lists the false hits at tau by answer pair, most frequent first.
func (r *Replay) Confusions(tau float64) []Confusion {
	byPair := map[[2]string]*Confusion{}
	for _, o := range r.Outcomes {
		if !o.Hit(tau) || o.Correct {
			continue
		}
		a := r.Corpus.Prompts[o.Prompt]
		b := r.Corpus.Prompts[r.Outcomes[o.Neighbor].Prompt]
		key := [2]string{a.Answer(), b.Answer()}
		if byPair[key] == nil {
			category, kind := classify(a, b)
			byPair[key] = &Confusion{Request: key[0], Neighbor: key[1], Category: category, Kind: kind}
		}
		byPair[key].Count++
	}
	out := make([]Confusion, 0, len(byPair))
	for _, c := range byPair {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(x, y Confusion) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Request, y.Request), cmp.Compare(x.Neighbor, y.Neighbor))
	})
	return out
}

func classify(a, b Prompt) (category, kind string) {
	switch {
	case a.Intent == b.Intent:
		return "language", ""
	case a.Family == b.Family:
		return "trap", a.Kind
	default:
		return "unrelated", ""
	}
}

func percentiles(sample []int64) (p50, p95 time.Duration) {
	slices.Sort(sample)
	return time.Duration(report.Percentile(sample, 50)), time.Duration(report.Percentile(sample, 95))
}

func ratio(num, den int) float64 {
	if den == 0 {
		return math.NaN()
	}
	return float64(num) / float64(den)
}

func usd(v float64) float64 { return math.Round(v*1e9) / 1e9 }
