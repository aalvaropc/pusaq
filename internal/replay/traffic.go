package replay

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/aalvaropc/pusaq/internal/mockprovider"
)

// Zipf draws ranks 0..n-1 with probability proportional to 1/(rank+1)^s.
type Zipf struct{ cdf []float64 }

// NewZipf builds the sampler for n ranks and exponent s ≥ 0.
func NewZipf(n int, s float64) (*Zipf, error) {
	if n < 1 {
		return nil, fmt.Errorf("zipf needs at least one rank, got %d", n)
	}
	if !(s >= 0) || math.IsInf(s, 1) {
		return nil, fmt.Errorf("zipf exponent must be a finite number ≥ 0, got %g", s)
	}
	cdf := make([]float64, n)
	sum := 0.0
	for k := range n {
		sum += math.Pow(float64(k+1), -s)
		cdf[k] = sum
	}
	for k := range cdf {
		cdf[k] /= sum
	}
	cdf[n-1] = 1
	return &Zipf{cdf: cdf}, nil
}

// Prob returns the probability of rank k.
func (z *Zipf) Prob(k int) float64 {
	if k == 0 {
		return z.cdf[0]
	}
	return z.cdf[k] - z.cdf[k-1]
}

// Rank maps u in [0,1) to its rank.
func (z *Zipf) Rank(u float64) int {
	return sort.Search(len(z.cdf)-1, func(i int) bool { return z.cdf[i] > u })
}

// Request is one replayed request: a corpus prompt and the provider latency it waits for.
type Request struct {
	Prompt   int
	Provider time.Duration
}

// TrafficOptions parameterize the synthetic request sequence.
type TrafficOptions struct {
	Requests int
	Exponent float64
	Seed     uint64
	Latency  mockprovider.Options
}

// Traffic draws a deterministic request sequence: answers ranked by a seeded permutation and drawn by Zipf, paraphrases uniform, provider latencies from the mock's model on a separate stream.
func Traffic(c *Corpus, o TrafficOptions) ([]Request, error) {
	if o.Requests < 1 {
		return nil, errors.New("traffic needs at least one request")
	}
	if err := o.Latency.Validate(); err != nil {
		return nil, err
	}
	z, err := NewZipf(len(c.Answers), o.Exponent)
	if err != nil {
		return nil, err
	}
	draw := rand.New(rand.NewPCG(o.Seed, 1))
	wait := rand.New(rand.NewPCG(o.Seed, 2))
	rank := draw.Perm(len(c.Answers))
	reqs := make([]Request, o.Requests)
	for i := range reqs {
		group := c.Answers[rank[z.Rank(draw.Float64())]]
		reqs[i] = Request{Prompt: group[draw.IntN(len(group))], Provider: o.Latency.SampleLatency(wait.Float64)}
	}
	return reqs, nil
}
