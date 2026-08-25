// Package embed is the embedding seam of the semantic cache — the same
// shape as the provider seam: an interface the gateway consumes, with a
// deterministic local implementation for $0 offline work and an HTTP
// implementation for any OpenAI-compatible /embeddings endpoint.
package embed

import (
	"context"
	"hash/fnv"
	"math"
)

// Embedder turns text into a normalized vector. Implementations MUST return
// L2-normalized vectors so cosine similarity reduces to a dot product.
type Embedder interface {
	// Name identifies the embedder in records — published numbers must say
	// which embedder produced them.
	Name() string
	// Embed returns the L2-normalized embedding of text.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// LocalDim is the Local embedder's fixed dimensionality.
const LocalDim = 256

// Local is a deterministic char-trigram feature-hashing embedder: free,
// offline, and stable across runs — it exercises the cache machinery
// honestly, but its similarity is lexical, not semantic. Numbers produced
// with it measure the instrument, not paraphrase quality.
type Local struct{}

// Name implements Embedder.
func (Local) Name() string { return "local" }

// Embed implements Embedder: hash every character trigram into a fixed-dim
// vector, then L2-normalize. Identical texts embed identically; disjoint
// texts share (almost) no features.
func (Local) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, LocalDim)
	runes := []rune(text)
	if len(runes) < 3 {
		runes = append(runes, ' ', ' ')
	}
	for i := 0; i+3 <= len(runes); i++ {
		h := fnv.New32a()
		_, _ = h.Write([]byte(string(runes[i : i+3])))
		vec[h.Sum32()%LocalDim]++
	}
	return Normalize(vec), nil
}

// Normalize L2-normalizes v in place and returns it. A zero vector is
// returned unchanged (its similarity to anything is 0).
func Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := 1 / math.Sqrt(sum)
	for i := range v {
		v[i] = float32(float64(v[i]) * inv)
	}
	return v
}

// Cosine returns the cosine similarity of two normalized vectors (their dot
// product). Mismatched lengths score 0.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}
