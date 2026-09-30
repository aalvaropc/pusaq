// Package telemetry emits per-request cost events asynchronously. Telemetry
// observes the hot path but never participates in it: a slow or unavailable
// sink must never delay a user response, so overflow drops events (counted)
// instead of blocking (docs/design.md §5.7).
package telemetry

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aalvaropc/pusaq/internal/core"
)

// Sink receives cost events. Implementations may be slow; the Emitter
// shields the request path from them. The foundation sink writes slog JSON
// to stdout; a Pub/Sub sink can replace it without touching producers.
type Sink interface {
	Write(event core.CostEvent)
}

// SlogSink writes each cost event as one structured log line.
type SlogSink struct {
	Logger *slog.Logger
}

// Write implements Sink.
func (s SlogSink) Write(event core.CostEvent) {
	attrs := []slog.Attr{
		slog.String("request_id", event.RequestID),
		slog.Time("timestamp", event.Timestamp),
		slog.String("op", event.Op),
		slog.String("tenant", event.Tenant),
		slog.String("outcome", string(event.Outcome)),
		slog.String("error_code", string(event.ErrorCode)),
		slog.Int64("latency_ms", event.Latency.Milliseconds()),
	}
	if event.RequestedModel != "" {
		attrs = append(attrs,
			slog.String("requested_model", event.RequestedModel),
			slog.String("served_model", event.ServedModel),
			slog.String("route_reason", event.RouteReason),
			slog.Int("prompt_tokens", event.Usage.PromptTokens),
			slog.Int("completion_tokens", event.Usage.CompletionTokens),
		)
	}
	if event.FeatureTag != "" {
		attrs = append(attrs, slog.String("feature_tag", event.FeatureTag))
	}
	if event.RunID != "" {
		attrs = append(attrs, slog.String("run_id", event.RunID))
	}
	if event.Region != "" {
		attrs = append(attrs, slog.String("region", event.Region))
	}
	if len(event.PolicyFlags) > 0 {
		attrs = append(attrs, slog.String("policy_flags", strings.Join(event.PolicyFlags, ",")))
	}
	if event.CacheSkip != "" {
		attrs = append(attrs, slog.String("cache_skip", event.CacheSkip))
	}
	if event.TokensCharged > 0 {
		attrs = append(attrs, slog.Int("tokens_charged", event.TokensCharged))
	}
	if event.ServedProvider != "" {
		attrs = append(attrs,
			slog.String("served_provider", event.ServedProvider),
			slog.Int("attempts", event.Attempts),
		)
	}
	if event.Cost.TableVersion != "" {
		attrs = append(attrs,
			slog.Float64("cost_usd", event.Cost.USD),
			slog.String("price_table_version", event.Cost.TableVersion),
		)
	}
	if event.SpendUSDMonth > 0 {
		attrs = append(attrs, slog.Float64("spend_usd_month", event.SpendUSDMonth))
	}
	s.Logger.LogAttrs(context.Background(), slog.LevelInfo, "cost_event", attrs...)
}

// Emitter delivers cost events to a Sink from a single drain goroutine.
// Emit never blocks and never panics: when the buffer is full — or the
// emitter is closed — the event is dropped and the drop counter incremented.
type Emitter struct {
	sink    Sink
	events  chan core.CostEvent
	dropped atomic.Uint64
	closed  atomic.Bool

	closeOnce sync.Once
	quit      chan struct{}
	done      chan struct{}
}

// NewEmitter starts an Emitter with the given buffer capacity.
func NewEmitter(sink Sink, buffer int) *Emitter {
	e := &Emitter{
		sink:   sink,
		events: make(chan core.CostEvent, buffer),
		quit:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go e.drain()
	return e
}

// drain delivers events until Close, then flushes whatever the buffer holds.
// The events channel is never closed, so Emit can never panic.
func (e *Emitter) drain() {
	defer close(e.done)
	for {
		select {
		case event := <-e.events:
			e.sink.Write(event)
		case <-e.quit:
			for {
				select {
				case event := <-e.events:
					e.sink.Write(event)
				default:
					return
				}
			}
		}
	}
}

// Emit enqueues an event without ever blocking the caller. Events offered
// while the buffer is full, or after Close, are dropped and counted.
func (e *Emitter) Emit(event core.CostEvent) {
	if e.closed.Load() {
		e.dropped.Add(1)
		return
	}
	select {
	case e.events <- event:
	default:
		e.dropped.Add(1)
	}
}

// Dropped reports how many events have been dropped so far.
func (e *Emitter) Dropped() uint64 { return e.dropped.Load() }

// Close stops intake, flushes already-buffered events to the sink, and
// returns once the drain goroutine has exited. Safe to call more than once.
// The gateway calls it after the HTTP server has fully drained, so no
// producers remain by the time events stop being accepted.
func (e *Emitter) Close() {
	e.closeOnce.Do(func() {
		e.closed.Store(true)
		close(e.quit)
	})
	<-e.done
}
