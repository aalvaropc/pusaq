package semcache

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type sensitiveEmbedder struct{}

func (sensitiveEmbedder) Name() string { return "test" }
func (sensitiveEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("private-embedding-sentinel")
}

func TestEmbeddingDiagnosticsExcludeRawError(t *testing.T) {
	var logs bytes.Buffer
	s := NewShadow(sensitiveEmbedder{}, NewIndex(10), slog.New(slog.NewJSONHandler(&logs, nil)), 0.9, 1, 1)
	s.Offer(Sample{RequestID: "request", Prompt: "private-embedding-sentinel"})
	s.Close()
	if s.Skipped() != 1 || strings.Contains(logs.String(), "private-embedding-sentinel") {
		t.Fatal("embedding diagnostics leaked or failure was not counted")
	}
}
