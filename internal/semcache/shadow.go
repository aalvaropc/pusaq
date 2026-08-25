package semcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aalvaropc/pusaq/internal/embed"
)

// evalBudget bounds one embedding call inside the worker; a slow embedder
// skips that evaluation, it never queues work behind itself indefinitely.
const evalBudget = 5 * time.Second

// Sample is one shadow evaluation request, captured at request finalize.
type Sample struct {
	RequestID string
	Namespace string
	// Prompt is the user message content — the text being embedded.
	Prompt string
	// Fingerprint identifies the response this request actually produced.
	Fingerprint string
}

// Shadow evaluates samples asynchronously: embed → nearest neighbor →
// record → insert. It never serves anything and never blocks its producers:
// a full queue drops the sample and counts the drop (the telemetry
// discipline — losing a measurement is acceptable, delaying a request is
// not).
type Shadow struct {
	embedder embed.Embedder
	index    *Index
	logger   *slog.Logger
	tau      float64

	queue   chan Sample
	dropped atomic.Uint64
	skipped atomic.Uint64
	wg      sync.WaitGroup

	closeOnce sync.Once
}

// NewShadow starts a shadow evaluator with the given worker count and
// queue capacity.
func NewShadow(embedder embed.Embedder, index *Index, logger *slog.Logger, tau float64, workers, queueCap int) *Shadow {
	s := &Shadow{
		embedder: embedder,
		index:    index,
		logger:   logger,
		tau:      tau,
		queue:    make(chan Sample, queueCap),
	}
	for range workers {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

// Offer enqueues a sample without ever blocking; saturation drops it and
// counts the drop.
func (s *Shadow) Offer(sample Sample) {
	select {
	case s.queue <- sample:
	default:
		s.dropped.Add(1)
	}
}

// Dropped reports samples lost to saturation; Skipped reports evaluations
// abandoned on embedder failure.
func (s *Shadow) Dropped() uint64 { return s.dropped.Load() }

// Skipped reports evaluations abandoned because the embedder failed.
func (s *Shadow) Skipped() uint64 { return s.skipped.Load() }

// Close stops intake and waits for queued evaluations to finish.
func (s *Shadow) Close() {
	s.closeOnce.Do(func() { close(s.queue) })
	s.wg.Wait()
}

func (s *Shadow) worker() {
	defer s.wg.Done()
	for sample := range s.queue {
		s.evaluate(sample)
	}
}

// evaluate is the whole shadow cycle for one sample. It records a
// cache_shadow event whenever a neighbor exists — hit or miss — so any τ
// can be swept offline from the record stream alone.
func (s *Shadow) evaluate(sample Sample) {
	ctx, cancel := context.WithTimeout(context.Background(), evalBudget)
	start := time.Now()
	vec, err := s.embedder.Embed(ctx, sample.Prompt)
	cancel()
	embedLatency := time.Since(start)
	if err != nil {
		s.skipped.Add(1)
		s.logger.Warn("shadow evaluation skipped: embedder failed",
			slog.String("request_id", sample.RequestID),
			slog.Any("error", err),
		)
		return
	}

	if n := s.index.Nearest(sample.Namespace, vec); n != nil {
		s.logger.LogAttrs(context.Background(), slog.LevelInfo, "cache_shadow",
			slog.String("request_id", sample.RequestID),
			slog.String("namespace", sample.Namespace),
			slog.Bool("shadow_hit", n.Similarity >= s.tau),
			slog.Float64("best_similarity", n.Similarity),
			slog.String("neighbor_id", n.Entry.ID),
			slog.String("neighbor_fingerprint", n.Entry.Fingerprint),
			slog.String("response_fingerprint", sample.Fingerprint),
			slog.String("embedder", s.embedder.Name()),
			slog.Int64("embed_latency_ms", embedLatency.Milliseconds()),
		)
	}

	s.index.Insert(&Entry{
		ID:          sample.RequestID,
		Namespace:   sample.Namespace,
		Vector:      vec,
		Prompt:      sample.Prompt,
		Fingerprint: sample.Fingerprint,
	})
}

// Namespace derives the cache namespace per constitution rule 2: tenant,
// served physical model, and system-prompt content, with explicit
// placeholders for fields the API subset does not carry yet (tools,
// response format, temperature, policy version) so the derivation extends
// mechanically. Language is content, not configuration: it lives in the
// embedding space.
func Namespace(tenant, servedModel, systemPrompt string) string {
	h := sha256.New()
	for _, part := range []string{tenant, servedModel, systemPrompt, "tools=none", "format=none", "temp=none", "policy=v0"} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Fingerprint identifies response content for false-positive analysis:
// identical fingerprints on a would-be hit mean a certainly-correct answer;
// different ones mark a candidate false positive for offline review.
func Fingerprint(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}
