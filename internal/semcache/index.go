// Package semcache implements the shadow-mode semantic cache: an in-process
// measurement instrument that records what a serving cache would have done —
// without ever serving anything (docs/design.md §5.4, constitution rule 3).
package semcache

import (
	"container/list"
	"sync"

	"github.com/aalvaropc/pusaq/internal/embed"
)

// Entry is one cached observation: an embedded prompt and the fingerprint
// of the response it produced.
type Entry struct {
	ID          string
	Namespace   string
	Vector      []float32
	Prompt      string
	Fingerprint string
}

// Neighbor is a KNN result.
type Neighbor struct {
	Entry      *Entry
	Similarity float64
}

// Index is an exact, in-process flat cosine index partitioned by namespace,
// with a global LRU cap. Per replica by design: shadow measures per-process
// and the serving-mode slice will choose the shared design with data in
// hand. Safe for concurrent use.
type Index struct {
	mu   sync.Mutex
	max  int
	lru  *list.List               // front = most recent; values are *Entry
	pos  map[string]*list.Element // entry ID → LRU position
	byNS map[string][]*Entry
}

// NewIndex builds an empty index capped at max entries.
func NewIndex(max int) *Index {
	return &Index{
		max:  max,
		lru:  list.New(),
		pos:  make(map[string]*list.Element),
		byNS: make(map[string][]*Entry),
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
func (ix *Index) Insert(e *Entry) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.lru.Len() >= ix.max {
		oldest := ix.lru.Back()
		if oldest != nil {
			ix.evictLocked(oldest.Value.(*Entry))
		}
	}
	ix.pos[e.ID] = ix.lru.PushFront(e)
	ix.byNS[e.Namespace] = append(ix.byNS[e.Namespace], e)
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
	}
	entries := ix.byNS[e.Namespace]
	for i, cand := range entries {
		if cand.ID == e.ID {
			entries[i] = entries[len(entries)-1]
			ix.byNS[e.Namespace] = entries[:len(entries)-1]
			break
		}
	}
	if len(ix.byNS[e.Namespace]) == 0 {
		delete(ix.byNS, e.Namespace)
	}
}
