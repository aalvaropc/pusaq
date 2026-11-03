package semcache

import (
	"log/slog"
	"testing"

	"github.com/aalvaropc/pusaq/internal/embed"
)

func TestNeighborHitAtTheThreshold(t *testing.T) {
	n := &Neighbor{Entry: &Entry{ID: "a"}, Similarity: 0.9}
	if !n.Hit(0.9) {
		t.Fatal("similarity equal to tau is not a hit")
	}
	if n.Hit(0.9000001) {
		t.Fatal("similarity below tau is a hit")
	}
	var none *Neighbor
	if none.Hit(0) {
		t.Fatal("a missing neighbor is a hit")
	}
}

func TestEvaluateLooksUpBeforeInserting(t *testing.T) {
	cap := &capture{}
	s := NewShadow(embed.Local{}, NewIndex(10), cap.logger(), 0.9, 0, 0)
	defer s.Close()
	ns := baseKey().Namespace()

	first := s.Evaluate(Sample{RequestID: "r1", Namespace: ns, Prompt: "how do I cancel my subscription", Fingerprint: "fp1"})
	if first.Err != nil || first.Neighbor != nil || !first.Inserted {
		t.Fatalf("first evaluation = %+v, want no neighbor and an insertion", first)
	}
	second := s.Evaluate(Sample{RequestID: "r2", Namespace: ns, Prompt: "how do I cancel my subscription", Fingerprint: "fp2"})
	if second.Neighbor == nil || second.Neighbor.Entry.ID != "r1" || !second.Neighbor.Hit(0.999) {
		t.Fatalf("second evaluation = %+v, want r1 as an identical neighbor", second)
	}
	if second.Neighbor.Entry.Fingerprint != "fp1" || !second.Inserted {
		t.Fatalf("second evaluation = %+v, want r1's fingerprint and an insertion", second)
	}
	if second.EmbedLatency <= 0 || second.SearchLatency <= 0 {
		t.Fatalf("latencies = %s embed, %s search, want both measured", second.EmbedLatency, second.SearchLatency)
	}
	recs := cap.byMsg("cache_shadow")
	if len(recs) != 1 || recs[0]["request_id"] != "r2" || recs[0]["shadow_hit"] != true {
		t.Fatalf("records = %+v, want one hit record for r2", recs)
	}
}

func TestEvaluateReportsEmbedderFailure(t *testing.T) {
	s := NewShadow(failEmbedder{}, NewIndex(10), slog.New(slog.DiscardHandler), 0.9, 0, 0)
	defer s.Close()
	ev := s.Evaluate(Sample{RequestID: "r1", Namespace: "ns", Prompt: "p"})
	if ev.Err == nil || ev.Neighbor != nil || ev.Inserted || s.Skipped() != 1 {
		t.Fatalf("evaluation = %+v skipped=%d, want the error, no insertion, one skip", ev, s.Skipped())
	}
}
