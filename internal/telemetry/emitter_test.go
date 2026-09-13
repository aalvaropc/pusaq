package telemetry

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

// collectSink records events; optional gate blocks Write until released,
// simulating a slow sink.
type collectSink struct {
	mu     sync.Mutex
	events []core.CostEvent
	gate   chan struct{}
}

func (s *collectSink) Write(event core.CostEvent) {
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *collectSink) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func event(id string) core.CostEvent {
	return core.CostEvent{RequestID: id, Timestamp: time.Now(), Op: "test", Outcome: core.OutcomeSuccess}
}

func TestEmitNeverBlocksOnSlowSink(t *testing.T) {
	sink := &collectSink{gate: make(chan struct{})}
	e := NewEmitter(sink, 1)
	t.Cleanup(func() { close(sink.gate); e.Close() })

	// The sink is stalled: one event may be in the drain goroutine's hands
	// and one in the buffer; everything beyond must drop, not block.
	done := make(chan struct{})
	go func() {
		for range 100 {
			e.Emit(event("e"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked on a stalled sink")
	}
	if e.Dropped() == 0 {
		t.Fatal("expected drops with a stalled sink and full buffer")
	}
}

func TestFullBufferIncrementsDropCounter(t *testing.T) {
	sink := &collectSink{gate: make(chan struct{})}
	e := NewEmitter(sink, 2)

	const total = 10
	for range total {
		e.Emit(event("e"))
	}

	// Stalled sink holds at most one in-flight event + 2 buffered.
	if got := e.Dropped(); got < total-3 {
		t.Fatalf("Dropped() = %d, want at least %d", got, total-3)
	}
	close(sink.gate)
	e.Close()
	if delivered := sink.len(); delivered+int(e.Dropped()) != total {
		t.Fatalf("delivered %d + dropped %d != emitted %d", delivered, e.Dropped(), total)
	}
}

func TestCloseFlushesBufferedEvents(t *testing.T) {
	sink := &collectSink{}
	e := NewEmitter(sink, 16)

	const total = 10
	for range total {
		e.Emit(event("e"))
	}
	e.Close()

	if got := sink.len(); got != total {
		t.Fatalf("sink received %d events after Close, want %d", got, total)
	}
}

func TestEmitAfterCloseDropsWithoutPanic(t *testing.T) {
	e := NewEmitter(&collectSink{}, 1)
	e.Close()
	e.Emit(event("late"))
	if e.Dropped() != 1 {
		t.Fatalf("Dropped() = %d, want 1", e.Dropped())
	}
	e.Close() // idempotent
}

func TestSlogSinkWritesCostFieldsOnlyWhenPriced(t *testing.T) {
	var buf bytes.Buffer
	sink := SlogSink{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}

	priced := event("priced")
	priced.RequestedModel = "pusaq/fast"
	priced.Cost = core.Cost{USD: 5.6e-6, TableVersion: "v1"}
	sink.Write(priced)
	sink.Write(event("unpriced"))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if first["cost_usd"] != 5.6e-6 || first["price_table_version"] != "v1" {
		t.Fatalf("priced line = %v, want cost_usd and price_table_version", first)
	}
	if _, ok := second["cost_usd"]; ok {
		t.Fatalf("unpriced line carries cost_usd: %v", second)
	}
}

func TestSlogSinkWritesRoutingFields(t *testing.T) {
	var buf bytes.Buffer
	sink := SlogSink{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}

	e := event("routed")
	e.RequestedModel = "pusaq/auto"
	e.ServedModel = "model-b"
	e.FeatureTag = "reason"
	e.RouteReason = "rule:reason"
	sink.Write(e)

	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &line); err != nil {
		t.Fatal(err)
	}
	if line["route_reason"] != "rule:reason" || line["feature_tag"] != "reason" {
		t.Fatalf("line = %v, want route_reason and feature_tag", line)
	}
}
