package semcache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/aalvaropc/pusaq/internal/embed"
)

// vec builds a normalized 3-dim vector for geometric test cases.
func vec(x, y, z float32) []float32 {
	return embed.Normalize([]float32{x, y, z})
}

func TestNearestPicksTheClosest(t *testing.T) {
	ix := NewIndex(10)
	ix.Insert(&Entry{ID: "a", Namespace: "ns", Vector: vec(1, 0, 0)})
	ix.Insert(&Entry{ID: "b", Namespace: "ns", Vector: vec(0.9, 0.1, 0)})
	ix.Insert(&Entry{ID: "c", Namespace: "ns", Vector: vec(0, 1, 0)})

	n := ix.Nearest("ns", vec(1, 0.05, 0))
	if n == nil || n.Entry.ID != "a" {
		t.Fatalf("nearest = %+v, want a", n)
	}
	if n.Similarity < 0.99 {
		t.Fatalf("similarity = %g, want near 1", n.Similarity)
	}
}

func TestNamespaceIsolationInIndex(t *testing.T) {
	ix := NewIndex(10)
	ix.Insert(&Entry{ID: "a", Namespace: "ns1", Vector: vec(1, 0, 0)})
	if n := ix.Nearest("ns2", vec(1, 0, 0)); n != nil {
		t.Fatalf("cross-namespace neighbor = %+v, want nil", n)
	}
}

func TestLRUEvictionAtCap(t *testing.T) {
	ix := NewIndex(3)
	for i := range 3 {
		ix.Insert(&Entry{ID: fmt.Sprintf("e%d", i), Namespace: "ns", Vector: vec(float32(i+1), 1, 0)})
	}
	// Touch e0 so it is warm; e1 becomes the LRU.
	if n := ix.Nearest("ns", vec(1, 1, 0)); n == nil {
		t.Fatal("expected a neighbor")
	}
	warm := ix.Nearest("ns", vec(1, 1, 0)).Entry.ID

	ix.Insert(&Entry{ID: "e3", Namespace: "ns", Vector: vec(0, 0, 1)})
	if got := ix.Len(); got != 3 {
		t.Fatalf("len = %d, want cap 3", got)
	}
	// The warm entry survived.
	found := false
	for _, e := range ix.byNS["ns"] {
		if e.ID == warm {
			found = true
		}
	}
	if !found {
		t.Fatalf("warm entry %q was evicted", warm)
	}
}

func TestIndexConcurrencySafe(t *testing.T) {
	ix := NewIndex(100)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ix.Insert(&Entry{ID: fmt.Sprintf("e%d", i), Namespace: "ns", Vector: vec(float32(i), 1, 0)})
			_ = ix.Nearest("ns", vec(1, 0, 0))
			_ = ix.Len()
		}(i)
	}
	wg.Wait()
}

// capture collects slog records for assertions.
type capture struct {
	mu      sync.Mutex
	records []map[string]any
}

func (c *capture) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&captureWriter{c: c}, nil))
}

type captureWriter struct{ c *capture }

func (w *captureWriter) Write(p []byte) (int, error) {
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(p), &m); err == nil {
		w.c.mu.Lock()
		w.c.records = append(w.c.records, m)
		w.c.mu.Unlock()
	}
	return len(p), nil
}

func (c *capture) byMsg(msg string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, r := range c.records {
		if r["msg"] == msg {
			out = append(out, r)
		}
	}
	return out
}

func TestShadowHitAndMissRecords(t *testing.T) {
	cap := &capture{}
	// One worker: evaluations run in offer order, so r2 deterministically
	// finds r1 already inserted. With more workers, r2 can be evaluated
	// before r1's insert lands (the production shadow accepts that
	// occasional undercount; this test needs determinism).
	s := NewShadow(embed.Local{}, NewIndex(100), cap.logger(), 0.9, 1, 16)

	ns := Namespace("t1", "m1", "", "none")
	s.Offer(Sample{RequestID: "r1", Namespace: ns, Prompt: "how do I cancel my subscription", Fingerprint: "fp1"})
	s.Offer(Sample{RequestID: "r2", Namespace: ns, Prompt: "how do I cancel my subscription now", Fingerprint: "fp2"})
	s.Offer(Sample{RequestID: "r3", Namespace: ns, Prompt: "zzzz qqqq wwww completely different", Fingerprint: "fp3"})
	s.Close()

	recs := cap.byMsg("cache_shadow")
	// r1 has no neighbor (silent insert); r2 and r3 each record.
	if len(recs) != 2 {
		t.Fatalf("got %d cache_shadow records, want 2: %+v", len(recs), recs)
	}
	byID := map[string]map[string]any{}
	for _, r := range recs {
		byID[r["request_id"].(string)] = r
	}
	r2 := byID["r2"]
	if r2 == nil || r2["shadow_hit"] != true {
		t.Fatalf("r2 = %+v, want a shadow hit", r2)
	}
	if r2["neighbor_fingerprint"] != "fp1" || r2["response_fingerprint"] != "fp2" {
		t.Fatalf("r2 fingerprints = %+v", r2)
	}
	if r2["best_similarity"].(float64) < 0.9 {
		t.Fatalf("r2 similarity = %v", r2["best_similarity"])
	}
	r3 := byID["r3"]
	if r3 == nil || r3["shadow_hit"] != false {
		t.Fatalf("r3 = %+v, want a recorded miss", r3)
	}
	if r3["best_similarity"].(float64) >= 0.9 {
		t.Fatalf("r3 similarity = %v, want below tau", r3["best_similarity"])
	}
}

// stallEmbedder blocks until released, to fill the shadow queue.
type stallEmbedder struct{ gate chan struct{} }

func (s stallEmbedder) Name() string { return "stall" }

func (s stallEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	select {
	case <-s.gate:
		return []float32{1}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestShadowSaturationDropsAndCounts(t *testing.T) {
	gate := make(chan struct{})
	cap := &capture{}
	s := NewShadow(stallEmbedder{gate: gate}, NewIndex(10), cap.logger(), 0.9, 1, 2)

	for i := range 10 {
		s.Offer(Sample{RequestID: fmt.Sprintf("r%d", i), Namespace: "ns", Prompt: "p"})
	}
	if s.Dropped() == 0 {
		t.Fatal("no drops despite a stalled worker and a tiny queue")
	}
	close(gate)
	s.Close()
}

// failEmbedder always errors.
type failEmbedder struct{}

func (failEmbedder) Name() string { return "fail" }

func (failEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("no embeddings today")
}

func TestShadowEmbedderFailureSkipsAndCounts(t *testing.T) {
	cap := &capture{}
	s := NewShadow(failEmbedder{}, NewIndex(10), cap.logger(), 0.9, 1, 4)
	s.Offer(Sample{RequestID: "r1", Namespace: "ns", Prompt: "p"})
	s.Close()

	if s.Skipped() != 1 {
		t.Fatalf("skipped = %d, want 1", s.Skipped())
	}
	if len(cap.byMsg("cache_shadow")) != 0 {
		t.Fatal("a failed evaluation still produced a shadow record")
	}
}

func TestNamespaceDerivation(t *testing.T) {
	base := Namespace("t1", "m1", "sys", "none")
	if Namespace("t2", "m1", "sys", "none") == base {
		t.Fatal("different tenants share a namespace")
	}
	if Namespace("t1", "m2", "sys", "none") == base {
		t.Fatal("different models share a namespace")
	}
	if Namespace("t1", "m1", "other-sys", "none") == base {
		t.Fatal("different system prompts share a namespace")
	}
	if Namespace("t1", "m1", "sys", "none") != base {
		t.Fatal("namespace derivation is not deterministic")
	}
	// Temperature is part of the key: the provider default ("none"), an
	// explicit 0, and an explicit 1 are three answer spaces.
	if Namespace("t1", "m1", "sys", "0") == base || Namespace("t1", "m1", "sys", "1") == base || Namespace("t1", "m1", "sys", "0") == Namespace("t1", "m1", "sys", "1") {
		t.Fatal("different temperatures share a namespace")
	}
}

func TestShadowCloseDrainsQueue(t *testing.T) {
	cap := &capture{}
	s := NewShadow(embed.Local{}, NewIndex(100), cap.logger(), 0.9, 1, 16)
	ns := Namespace("t", "m", "", "none")
	s.Offer(Sample{RequestID: "a", Namespace: ns, Prompt: "one two three"})
	s.Offer(Sample{RequestID: "b", Namespace: ns, Prompt: "one two three four"})
	s.Close()
	// Both were evaluated: the second found the first as neighbor.
	if len(cap.byMsg("cache_shadow")) != 1 {
		t.Fatalf("records = %d, want 1 after drain", len(cap.byMsg("cache_shadow")))
	}
}
