package main

import (
	"errors"
	"flag"
	"math"
	"slices"
	"time"

	"github.com/aalvaropc/pusaq/internal/config"
	"github.com/aalvaropc/pusaq/internal/mockprovider"
)

const maxRequests = 100_000

type options struct {
	corpus       string
	out          string
	requests     int
	zipf         float64
	seed         uint64
	tau          float64
	sweepFrom    float64
	sweepTo      float64
	sweepStep    float64
	inputPrice   float64
	outputPrice  float64
	embedPrice   float64
	outputTokens int
	latencyP50   time.Duration
	latencyP99   time.Duration
	indexEntries int
	indexBytes   int64
}

func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("cachereplay", flag.ContinueOnError)
	fs.StringVar(&o.corpus, "corpus", "testdata/replay/intents.csv", "labeled corpus CSV (intent,lang,family,kind,text)")
	fs.StringVar(&o.out, "out", "out/replay", "output directory for sweep.csv, requests.csv and summary.md")
	fs.IntVar(&o.requests, "requests", 5000, "replayed requests, in [1, 100000]")
	fs.Float64Var(&o.zipf, "zipf", 1, "Zipf exponent over the corpus' answers, finite and ≥ 0 (0 is uniform)")
	fs.Uint64Var(&o.seed, "seed", 1, "seed of the traffic and provider-latency draws")
	fs.Float64Var(&o.tau, "tau", config.DefaultCacheTau, "similarity threshold reported in detail, in (0, 1]")
	fs.Float64Var(&o.sweepFrom, "sweep-from", 0.80, "lowest τ of the sweep")
	fs.Float64Var(&o.sweepTo, "sweep-to", 0.995, "highest τ of the sweep, at most 1")
	fs.Float64Var(&o.sweepStep, "sweep-step", 0.005, "τ step of the sweep")
	fs.Float64Var(&o.inputPrice, "input-price", 0.10, "provider input price, USD per 1M tokens")
	fs.Float64Var(&o.outputPrice, "output-price", 0.40, "provider output price, USD per 1M tokens")
	fs.Float64Var(&o.embedPrice, "embed-price", 0.02, "embedding price, USD per 1M tokens")
	fs.IntVar(&o.outputTokens, "output-tokens", 11, "completion tokens of every provider answer")
	fs.DurationVar(&o.latencyP50, "latency-p50", 200*time.Millisecond, "provider latency p50 of the mock provider's model")
	fs.DurationVar(&o.latencyP99, "latency-p99", 2*time.Second, "provider latency p99 of the mock provider's model")
	fs.IntVar(&o.indexEntries, "index-entries", config.DefaultCacheMaxEntries, "shadow index entry cap")
	fs.Int64Var(&o.indexBytes, "index-bytes", config.DefaultCacheMaxBytes, "shadow index retained-data cap in bytes")
	return fs
}

func (o options) validate() error {
	switch {
	case o.requests < 1 || o.requests > maxRequests:
		return errors.New("-requests must be in [1, 100000]")
	case !(o.zipf >= 0) || math.IsInf(o.zipf, 1):
		return errors.New("-zipf must be a finite number ≥ 0")
	case !(o.tau > 0 && o.tau <= 1):
		return errors.New("-tau must be in (0, 1]")
	case !(o.sweepFrom > 0 && o.sweepFrom <= o.sweepTo && o.sweepTo <= 1):
		return errors.New("-sweep-from and -sweep-to must satisfy 0 < from ≤ to ≤ 1")
	case !(o.sweepStep > 0) || (o.sweepTo-o.sweepFrom)/o.sweepStep > 1000:
		return errors.New("-sweep-step must be positive and give at most 1001 points")
	case !price(o.inputPrice) || !price(o.outputPrice) || !price(o.embedPrice):
		return errors.New("-input-price, -output-price and -embed-price must be finite and ≥ 0")
	case o.outputTokens < 0:
		return errors.New("-output-tokens must be ≥ 0")
	case o.indexEntries < 1 || o.indexBytes < 1:
		return errors.New("-index-entries and -index-bytes must be positive")
	}
	return o.latency().Validate()
}

func price(v float64) bool { return v >= 0 && !math.IsInf(v, 1) }

func (o options) latency() mockprovider.Options {
	return mockprovider.Options{LatencyP50: o.latencyP50, LatencyP99: o.latencyP99}
}

func (o options) taus() []float64 {
	var out []float64
	for k := 0; ; k++ {
		t := math.Round((o.sweepFrom+float64(k)*o.sweepStep)*1e6) / 1e6
		if t > o.sweepTo+1e-9 {
			break
		}
		out = append(out, t)
	}
	if !slices.Contains(out, o.tau) {
		out = append(out, o.tau)
		slices.Sort(out)
	}
	return out
}
