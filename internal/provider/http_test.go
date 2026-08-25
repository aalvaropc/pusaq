package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestStreamCompleteReturnsRawSSE(t *testing.T) {
	const sse = "data: {\"object\":\"chat.completion.chunk\"}\n\ndata: [DONE]\n\n"
	var sawStreamTrue bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sawStreamTrue = strings.Contains(string(body), `"stream":true`)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
	stream, err := p.StreamComplete(context.Background(), chatReq())
	if err != nil {
		t.Fatalf("StreamComplete() error: %v", err)
	}
	defer func() { _ = stream.Close() }()

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	if string(got) != sse {
		t.Fatalf("stream = %q, want raw SSE passthrough", got)
	}
	if !sawStreamTrue {
		t.Fatal("upstream request did not set stream:true")
	}
}

func TestStreamComplete500IsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
	_, err := p.StreamComplete(context.Background(), chatReq())
	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
		t.Fatalf("err = %v, want %s", err, core.CodeUpstream)
	}
}

func TestStreamCompleteHeaderTimeoutBounded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // never sends headers
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, 50*time.Millisecond, discardLogger())
	_, err := p.StreamComplete(context.Background(), chatReq())
	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
		t.Fatalf("err = %v, want %s on header timeout", err, core.CodeUpstream)
	}
}

func TestStreamCompleteNotKilledByOverallTimeout(t *testing.T) {
	// Headers arrive instantly; the body takes longer than the timeout.
	// The stream client must not cut it (only time-to-first-byte is bounded).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond) // longer than the 50ms timeout
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, 50*time.Millisecond, discardLogger())
	stream, err := p.StreamComplete(context.Background(), chatReq())
	if err != nil {
		t.Fatalf("StreamComplete() error: %v", err)
	}
	defer func() { _ = stream.Close() }()
	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("stream cut mid-body: %v", err)
	}
	if !strings.Contains(string(got), "[DONE]") {
		t.Fatalf("incomplete stream: %q", got)
	}
}

func TestAPIKeySentAsBearerWhenConfigured(t *testing.T) {
	var buffered, streamed string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "event-stream") {
			streamed = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		buffered = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, upstreamBody)
	}))
	defer upstream.Close()

	p := NewHTTP("gemini", upstream.URL, time.Second, discardLogger(), WithAPIKey("sk-gem"))
	if _, err := p.Complete(context.Background(), chatReq()); err != nil {
		t.Fatal(err)
	}
	stream, err := p.StreamComplete(context.Background(), chatReq())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(stream)
	_ = stream.Close()

	if buffered != "Bearer sk-gem" || streamed != "Bearer sk-gem" {
		t.Fatalf("auth headers = buffered %q / streamed %q", buffered, streamed)
	}
}

func TestNoAPIKeyMeansNoAuthHeader(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, upstreamBody)
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
	if _, err := p.Complete(context.Background(), chatReq()); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("Authorization = %q, want none", got)
	}
}
