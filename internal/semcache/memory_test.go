package semcache

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIndexByteBudgetAndReplacement(t *testing.T) {
	ix := NewIndex(10, 128)
	if !ix.Insert(&Entry{ID: "a", Namespace: "n", Vector: make([]float32, 16)}) || !ix.Insert(&Entry{ID: "b", Namespace: "n", Vector: make([]float32, 16)}) {
		t.Fatal("bounded entry rejected")
	}
	if ix.Len() != 1 || ix.bytes > 128 || ix.Nearest("n", nil).Entry.ID != "b" {
		t.Fatal("byte eviction failed")
	}
	if ix.Insert(&Entry{ID: "large", Namespace: "n", Vector: make([]float32, 128)}) {
		t.Fatal("oversized entry retained")
	}
	if !ix.Insert(&Entry{ID: "b", Namespace: "n", Vector: []float32{1}}) || ix.Len() != 1 || ix.bytes > 128 {
		t.Fatal("replacement broke accounting")
	}
}

func TestIndexOwnsVectorsAndClearsEvictedReferences(t *testing.T) {
	ix := NewIndex(10, 1024)
	vector := []float32{1, 0}
	ix.Insert(&Entry{ID: "a", Namespace: "n", Vector: vector})
	ix.Insert(&Entry{ID: "b", Namespace: "n", Vector: []float32{0, 1}})
	vector[0] = 0
	if ix.Nearest("n", []float32{1, 0}).Entry.ID != "a" {
		t.Fatal("caller changed retained vector")
	}
	ix.mu.Lock()
	entries := ix.byNS["n"]
	ix.evictLocked(entries[0])
	if entries[len(entries)-1] != nil {
		t.Fatal("evicted entry remains referenced")
	}
	ix.mu.Unlock()
}

type gatedMemoryEmbedder struct {
	started chan struct{}
	gate    chan struct{}
}

func (g gatedMemoryEmbedder) Name() string { return "test" }
func (g gatedMemoryEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	select {
	case g.started <- struct{}{}:
	default:
	}
	select {
	case <-g.gate:
		return []float32{1, 0}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestShadowByteBudgetCountsInflightAndReturnsCredits(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	s := NewShadow(gatedMemoryEmbedder{started, gate}, NewIndex(10), slog.New(slog.NewTextHandler(io.Discard, nil)), 0.9, 1, 1, 96)
	s.Offer(Sample{Prompt: strings.Repeat("x", 32)})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	s.Offer(Sample{Prompt: strings.Repeat("y", 32)})
	s.Offer(Sample{Prompt: strings.Repeat("z", 32)})
	if s.pendingBytes.Load() != 64 || s.Dropped() != 1 {
		t.Fatalf("bytes=%d dropped=%d", s.pendingBytes.Load(), s.Dropped())
	}
	s.Offer(Sample{Prompt: strings.Repeat("q", 97)})
	if s.pendingBytes.Load() != 64 || s.Dropped() != 2 {
		t.Fatal("byte capacity was not enforced")
	}
	close(gate)
	s.Close()
	if s.pendingBytes.Load() != 0 {
		t.Fatal("drain retained byte credits")
	}
	s.Offer(Sample{Prompt: "closed"})
	if s.pendingBytes.Load() != 0 || s.Dropped() != 3 {
		t.Fatal("closed evaluator accepted sample")
	}
}

func TestShadowConcurrentOffersAndClose(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	s := NewShadow(gatedMemoryEmbedder{make(chan struct{}, 1), gate}, NewIndex(10, 512), slog.New(slog.NewTextHandler(io.Discard, nil)), 0.9, 2, 8, 512)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s.Offer(Sample{Prompt: strings.Repeat("x", 128)})
				if s.pendingBytes.Load() > 512 {
					t.Error("pending byte budget exceeded")
				}
			}
		}()
	}
	s.Close()
	wg.Wait()
	if s.pendingBytes.Load() != 0 || s.index.bytes > 512 {
		t.Fatal("shutdown retained samples or exceeded index budget")
	}
}

func TestShadowIndexExcludesRawPromptsAndCountsOversizedVectors(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	for _, budget := range []int64{1024, 4} {
		ix := NewIndex(10, budget)
		s := NewShadow(gatedMemoryEmbedder{make(chan struct{}, 1), gate}, ix, slog.New(slog.NewTextHandler(io.Discard, nil)), 0.9, 1, 1)
		s.Offer(Sample{RequestID: "request", Namespace: "n", Prompt: "private-retention-sentinel"})
		s.Close()
		if budget == 4 {
			if ix.Len() != 0 || s.Skipped() != 1 {
				t.Fatal("oversized observation was not skipped")
			}
			continue
		}
		raw, err := json.Marshal(ix.Nearest("n", []float32{1, 0}).Entry)
		if err != nil || strings.Contains(string(raw), "private-retention-sentinel") {
			t.Fatal("index retained raw prompt")
		}
	}
}
