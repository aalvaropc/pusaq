package replay

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/embed"
	"github.com/aalvaropc/pusaq/internal/policy"
	"github.com/aalvaropc/pusaq/internal/pricing"
	"github.com/aalvaropc/pusaq/internal/semcache"
)

// Prices are USD per million tokens.
type Prices struct {
	Input  float64
	Output float64
	Embed  float64
}

// Config parameterizes a replay.
type Config struct {
	Prices       Prices
	OutputTokens int
	IndexEntries int
	IndexBytes   int64
}

// Outcome is what one replayed request measured.
type Outcome struct {
	Request
	ID          string
	Skip        string
	Neighbor    int
	Similarity  float64
	Correct     bool
	Repeat      bool
	Inserted    bool
	Lookup      time.Duration
	ProviderUSD float64
	EmbedUSD    float64
}

// Hit reports whether the request is a predicted hit at tau, by the shadow's rule.
func (o Outcome) Hit(tau float64) bool {
	return o.Skip == "" && o.Neighbor >= 0 && (&semcache.Neighbor{Similarity: o.Similarity}).Hit(tau)
}

// Replay is the per-request record of one run; any τ is evaluated from it.
type Replay struct {
	Corpus   *Corpus
	Embedder string
	Outcomes []Outcome
}

const (
	chatPrice  = "chat"
	embedPrice = "embedding"
)

var namespace = semcache.Key{Tenant: "replay", RouteModel: "replay-route", Temperature: "none", MaxTokens: "none"}.Namespace()

// Run replays the requests in order through the shadow evaluation path: content-rule eligibility, then embed, nearest neighbor and insertion.
func Run(e embed.Embedder, c *Corpus, reqs []Request, cfg Config) (*Replay, error) {
	if cfg.IndexEntries < 1 || cfg.IndexBytes < 1 {
		return nil, errors.New("index entries and bytes must be positive")
	}
	if cfg.OutputTokens < 0 {
		return nil, errors.New("output tokens must be ≥ 0")
	}
	prices := pricing.NewTable("replay", map[string]pricing.Price{
		chatPrice:  {InputPerM: cfg.Prices.Input, OutputPerM: cfg.Prices.Output},
		embedPrice: {InputPerM: cfg.Prices.Embed},
	})
	shadow := semcache.NewShadow(e, semcache.NewIndex(cfg.IndexEntries, cfg.IndexBytes), slog.New(slog.DiscardHandler), 1, 0, 0)
	defer shadow.Close()

	seen := map[int]bool{}
	byID := map[string]int{}
	out := make([]Outcome, len(reqs))
	for i, req := range reqs {
		if req.Prompt < 0 || req.Prompt >= len(c.Prompts) {
			return nil, fmt.Errorf("request %d: prompt %d outside the corpus", i, req.Prompt)
		}
		p := c.Prompts[req.Prompt]
		o := Outcome{Request: req, ID: fmt.Sprintf("r%07d", i), Neighbor: -1}
		o.ProviderUSD, _ = prices.Cost(chatPrice, core.Usage{PromptTokens: tokens(len("user") + len(p.Text)), CompletionTokens: cfg.OutputTokens})
		if o.Skip = skipReason(p.Text); o.Skip != "" {
			out[i] = o
			continue
		}
		o.EmbedUSD, _ = prices.Cost(embedPrice, core.Usage{PromptTokens: tokens(len(p.Text))})
		answer := semcache.Fingerprint(p.Answer())
		ev := shadow.Evaluate(semcache.Sample{RequestID: o.ID, Namespace: namespace, Prompt: p.Text, Fingerprint: answer})
		if ev.Err != nil {
			return nil, fmt.Errorf("request %d: %w", i, ev.Err)
		}
		o.Lookup = ev.EmbedLatency + ev.SearchLatency
		o.Inserted = ev.Inserted
		o.Repeat = seen[req.Prompt]
		seen[req.Prompt] = true
		if ev.Neighbor != nil {
			o.Neighbor = byID[ev.Neighbor.Entry.ID]
			o.Similarity = ev.Neighbor.Similarity
			o.Correct = ev.Neighbor.Entry.Fingerprint == answer
		}
		byID[o.ID] = i
		out[i] = o
	}
	return &Replay{Corpus: c, Embedder: e.Name(), Outcomes: out}, nil
}

func tokens(bytes int) int { return (bytes + 3) / 4 }

func skipReason(text string) string {
	f := policy.Classify(text)
	switch {
	case len(f.Sensitive) > 0:
		return "sensitive"
	case len(f.Injection) > 0:
		return "injection"
	case len(f.Temporal) > 0:
		return "temporal"
	}
	return ""
}
