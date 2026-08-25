package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const upstreamBody = `{
  "id": "cmpl-1",
  "model": "mock-model",
  "choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}, "finish_reason": "stop"}],
  "usage": {"prompt_tokens": 3, "completion_tokens": 1}
}`

func chatReq() ChatRequest {
	return ChatRequest{
		Model:    "mock-model",
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
}

func TestCompleteRoundTrip(t *testing.T) {
	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, upstreamBody)
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
	resp, err := p.Complete(context.Background(), chatReq())
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if resp.Model != "mock-model" || len(resp.Choices) != 1 || resp.Usage.PromptTokens != 3 {
		t.Fatalf("resp = %+v", resp)
	}
	if gotBody == "" {
		t.Fatal("upstream received empty body")
	}
}

func TestCompleteUpstream500IsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `secret internal details`)
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
	_, err := p.Complete(context.Background(), chatReq())

	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
		t.Fatalf("err = %v, want %s", err, core.CodeUpstream)
	}
	if norm.Status() != http.StatusBadGateway {
		t.Fatalf("Status() = %d, want 502", norm.Status())
	}
	// The upstream body must never leak into the client-facing message.
	if containsSecret := norm.Message == "secret internal details"; containsSecret {
		t.Fatal("upstream body leaked into the normalized message")
	}
}

func TestCompleteTimeoutIsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, 50*time.Millisecond, discardLogger())
	_, err := p.Complete(context.Background(), chatReq())

	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
		t.Fatalf("err = %v, want %s on timeout", err, core.CodeUpstream)
	}
}

func TestCompleteMalformedUpstreamBodyIsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `not json at all`)
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
	_, err := p.Complete(context.Background(), chatReq())

	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
		t.Fatalf("err = %v, want %s on malformed body", err, core.CodeUpstream)
	}
}
