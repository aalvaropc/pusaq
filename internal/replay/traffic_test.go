package replay

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/mockprovider"
)

func TestZipfExactProbabilities(t *testing.T) {
	z, err := NewZipf(3, 1)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range []float64{6.0 / 11, 3.0 / 11, 2.0 / 11} {
		if got := z.Prob(k); math.Abs(got-want) > 1e-12 {
			t.Errorf("P(rank %d) = %g, want %g", k, got, want)
		}
	}
	uniform, err := NewZipf(4, 0)
	if err != nil {
		t.Fatal(err)
	}
	for k := range 4 {
		if got := uniform.Prob(k); math.Abs(got-0.25) > 1e-12 {
			t.Errorf("exponent 0: P(rank %d) = %g, want 0.25", k, got)
		}
	}
}

func TestZipfRankBoundaries(t *testing.T) {
	z, _ := NewZipf(3, 1)
	tests := []struct {
		u    float64
		want int
	}{{0, 0}, {math.Nextafter(z.cdf[0], 0), 0}, {z.cdf[0], 1}, {math.Nextafter(z.cdf[1], 0), 1}, {z.cdf[1], 2}, {math.Nextafter(1, 0), 2}}
	for _, tt := range tests {
		if got := z.Rank(tt.u); got != tt.want {
			t.Errorf("Rank(%g) = %d, want %d", tt.u, got, tt.want)
		}
	}
}

func TestZipfSampledShape(t *testing.T) {
	const n, draws = 10, 200_000
	z, _ := NewZipf(n, 1)
	rng := rand.New(rand.NewPCG(7, 7))
	counts := make([]int, n)
	for range draws {
		counts[z.Rank(rng.Float64())]++
	}
	for k := range n {
		p := z.Prob(k)
		sigma := math.Sqrt(draws * p * (1 - p))
		if math.Abs(float64(counts[k])-draws*p) > 5*sigma {
			t.Errorf("rank %d: %d draws, want %.0f ± %.0f", k, counts[k], draws*p, 5*sigma)
		}
		if k > 0 && counts[k] >= counts[k-1] {
			t.Errorf("rank %d drew %d, not fewer than rank %d's %d", k, counts[k], k-1, counts[k-1])
		}
	}
	if ratio := float64(counts[0]) / float64(counts[1]); math.Abs(ratio-2) > 0.05 {
		t.Errorf("rank 1 / rank 2 = %.3f, want 2 for exponent 1", ratio)
	}
}

func TestNewZipfRejectsInvalidParameters(t *testing.T) {
	for _, tt := range []struct {
		n int
		s float64
	}{{0, 1}, {3, -0.1}, {3, math.NaN()}, {3, math.Inf(1)}} {
		if _, err := NewZipf(tt.n, tt.s); err == nil {
			t.Errorf("NewZipf(%d, %g) accepted", tt.n, tt.s)
		}
	}
}

func trafficOptions(seed uint64) TrafficOptions {
	return TrafficOptions{Requests: 2000, Exponent: 1, Seed: seed, Latency: mockprovider.Options{LatencyP50: 200 * time.Millisecond, LatencyMax: 2 * time.Second}}
}

func prompts(reqs []Request) []int {
	out := make([]int, len(reqs))
	for i, r := range reqs {
		out[i] = r.Prompt
	}
	return out
}

func TestTrafficIsDeterministicPerSeed(t *testing.T) {
	c := loadCorpus(t)
	a, err := Traffic(c, trafficOptions(1))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Traffic(c, trafficOptions(1))
	if !slices.Equal(a, b) {
		t.Fatal("same seed produced different traffic")
	}
	other, _ := Traffic(c, trafficOptions(2))
	if slices.Equal(prompts(a), prompts(other)) {
		t.Fatal("different seeds produced the same prompts")
	}
	slower := trafficOptions(1)
	slower.Latency = mockprovider.Options{LatencyP50: time.Second, LatencyMax: 3 * time.Second}
	c2, _ := Traffic(c, slower)
	if !slices.Equal(prompts(a), prompts(c2)) {
		t.Fatal("latency parameters changed the request sequence")
	}
	for _, r := range a {
		if r.Provider < 100*time.Millisecond || r.Provider > 2*time.Second {
			t.Fatalf("provider latency %s outside the mock model's range", r.Provider)
		}
	}
}

func TestTrafficFollowsZipfOverAnswersAndUniformParaphrases(t *testing.T) {
	c, err := NewCorpus([]Prompt{
		{Intent: "a", Lang: "en", Family: "f", Kind: "entity", Text: "a0"},
		{Intent: "a", Lang: "en", Family: "f", Kind: "entity", Text: "a1"},
		{Intent: "a", Lang: "en", Family: "f", Kind: "entity", Text: "a2"},
		{Intent: "a", Lang: "en", Family: "f", Kind: "entity", Text: "a3"},
		{Intent: "b", Lang: "en", Family: "f", Kind: "entity", Text: "b0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	o := trafficOptions(3)
	o.Requests = 60_000
	reqs, err := Traffic(c, o)
	if err != nil {
		t.Fatal(err)
	}
	counts := make([]int, len(c.Prompts))
	for _, r := range reqs {
		counts[r.Prompt]++
	}
	answerA := counts[0] + counts[1] + counts[2] + counts[3]
	top, bottom := max(answerA, counts[4]), min(answerA, counts[4])
	if share := float64(top) / float64(o.Requests); math.Abs(share-2.0/3) > 0.01 {
		t.Errorf("top answer share = %.4f, want 2/3 for two answers at exponent 1", share)
	}
	if bottom == 0 {
		t.Fatal("the second answer was never drawn")
	}
	for i := range 4 {
		if share := float64(counts[i]) / float64(answerA); math.Abs(share-0.25) > 0.015 {
			t.Errorf("paraphrase %d share = %.4f, want 0.25", i, share)
		}
	}
}

func TestTrafficRejectsInvalidOptions(t *testing.T) {
	c := loadCorpus(t)
	bad := []TrafficOptions{
		{Requests: 0, Exponent: 1},
		{Requests: 1, Exponent: -1},
		{Requests: 1, Exponent: 1, Latency: mockprovider.Options{LatencyP50: time.Second, LatencyMax: time.Millisecond}},
	}
	for _, o := range bad {
		if _, err := Traffic(c, o); err == nil {
			t.Errorf("Traffic accepted %+v", o)
		}
	}
}
