// Package semcache implements the shadow-mode semantic cache: an in-process
// measurement instrument that records what a serving cache would have done —
// without ever serving anything (docs/design.md §5.4, constitution rule 3).
package semcache

import (
	"container/list"
	"strings"
	"sync"

	"github.com/aalvaropc/pusaq/internal/embed"
)

// Entry is one cached observation: an embedded prompt and the fingerprint
// of the response it produced.
type Entry struct {
	ID          string
	Namespace   string
	Vector      []float32
	Fingerprint string
}

// Neighbor is a KNN result.
type Neighbor struct {
	Entry      *Entry
	Similarity float64
}

// Index is an exact, in-process flat cosine index partitioned by namespace,
// with a global LRU cap. Per replica by design: each process measures the
// traffic it serves. Safe for concurrent use.
type Index struct {
	mu       sync.Mutex
	maxBytes int64
	bytes    int64
	max      int
	lru      *list.List               // front = most recent; values are *Entry
	pos      map[string]*list.Element // entry ID → LRU position
	byNS     map[string][]*Entry
}

// NewIndex builds an empty index capped at max entries.
func NewIndex(max int, byteLimits ...int64) *Index {
	maxBytes := int64(64 << 20)
	if len(byteLimits) > 0 {
		maxBytes = byteLimits[0]
	}
	return &Index{
		maxBytes: maxBytes,
		max:      max,
		lru:      list.New(),
		pos:      make(map[string]*list.Element),
		byNS:     make(map[string][]*Entry),
	}
}

// Nearest returns the most similar entry to vec within namespace, or nil
// when the namespace holds no entries. A returned neighbor is touched in
// the LRU (a would-be hit keeps its entry warm).
func (ix *Index) Nearest(namespace string, vec []float32) *Neighbor {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	var best *Entry
	bestSim := -2.0
	for _, e := range ix.byNS[namespace] {
		if sim := embed.Cosine(vec, e.Vector); sim > bestSim {
			best, bestSim = e, sim
		}
	}
	if best == nil {
		return nil
	}
	if el, ok := ix.pos[best.ID]; ok {
		ix.lru.MoveToFront(el)
	}
	return &Neighbor{Entry: best, Similarity: bestSim}
}

// Insert adds an entry, evicting the least-recently-used one at the cap.
func (ix *Index) Insert(e *Entry) bool {
	if e == nil {
		return false
	}
	size := entryBytes(e)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.max <= 0 || size > ix.maxBytes {
		return false
	}
	if old, ok := ix.pos[e.ID]; ok {
		ix.evictLocked(old.Value.(*Entry))
	}
	for ix.lru.Len() >= ix.max || size > ix.maxBytes-ix.bytes {
		oldest := ix.lru.Back()
		if oldest == nil {
			return false
		}
		ix.evictLocked(oldest.Value.(*Entry))
	}
	copyEntry := &Entry{ID: strings.Clone(e.ID), Namespace: strings.Clone(e.Namespace), Fingerprint: strings.Clone(e.Fingerprint), Vector: append([]float32(nil), e.Vector...)}
	ix.pos[copyEntry.ID] = ix.lru.PushFront(copyEntry)
	ix.byNS[copyEntry.Namespace] = append(ix.byNS[copyEntry.Namespace], copyEntry)
	ix.bytes += size
	return true
}

func entryBytes(e *Entry) int64 {
	return int64(len(e.ID)) + int64(len(e.Namespace)) + int64(len(e.Fingerprint)) + 4*int64(len(e.Vector))
}

// Len reports the number of live entries.
func (ix *Index) Len() int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.lru.Len()
}

// evictLocked removes an entry from every structure. Callers hold ix.mu.
func (ix *Index) evictLocked(e *Entry) {
	if el, ok := ix.pos[e.ID]; ok {
		ix.lru.Remove(el)
		delete(ix.pos, e.ID)
		ix.bytes -= entryBytes(e)
	}
	entries := ix.byNS[e.Namespace]
	for i, cand := range entries {
		if cand.ID == e.ID {
			entries[i] = entries[len(entries)-1]
			entries[len(entries)-1] = nil
			ix.byNS[e.Namespace] = entries[:len(entries)-1]
			break
		}
	}
	if len(ix.byNS[e.Namespace]) == 0 {
		delete(ix.byNS, e.Namespace)
	}
}
