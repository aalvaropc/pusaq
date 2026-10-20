package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
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

	p := NewHTTP("mock", upstream.URL+"/v1", time.Second, discardLogger())
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
	if gotBody == "" || strings.Contains(gotBody, "stream") {
		t.Fatalf("buffered upstream body = %q, want no stream fields", gotBody)
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

func TestPathsFollowTheVersionedBaseURL(t *testing.T) {
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, upstreamBody)
	}))
	defer upstream.Close()

	p := NewHTTP("gemini", upstream.URL+"/v1beta/openai", time.Second, discardLogger())
	if _, err := p.Complete(context.Background(), chatReq()); err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	stream, err := p.StreamComplete(context.Background(), chatReq())
	if err != nil {
		t.Fatalf("StreamComplete() error: %v", err)
	}
	_ = stream.Close()

	want := "/v1beta/openai/chat/completions"
	if len(paths) != 2 || paths[0] != want || paths[1] != want {
		t.Fatalf("paths = %q, want both calls at %s", paths, want)
	}
}

func TestStreamCompleteReturnsRawSSE(t *testing.T) {
	const sse = "data: {\"object\":\"chat.completion.chunk\"}\n\ndata: [DONE]\n\n"
	var sent struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
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
	if sent.Model != "mock-model" || !sent.Stream || !sent.StreamOptions.IncludeUsage {
		t.Fatalf("upstream request = %+v, want stream with include_usage", sent)
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for range 6 {
			time.Sleep(60 * time.Millisecond)
			_, _ = io.WriteString(w, "data: {}\n\n")
			w.(http.Flusher).Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, 200*time.Millisecond, discardLogger())
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

func TestStreamCompleteIdleTimeoutEndsStalledStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, 100*time.Millisecond, discardLogger())
	stream, err := p.StreamComplete(context.Background(), chatReq())
	if err != nil {
		t.Fatalf("StreamComplete() error: %v", err)
	}
	defer func() { _ = stream.Close() }()

	type result struct {
		got []byte
		err error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		got, err := io.ReadAll(stream)
		done <- result{got, err}
	}()
	var got []byte
	select {
	case r := <-done:
		got, err = r.got, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("stalled stream never ended")
	}
	if !errors.Is(err, errStreamIdle) {
		t.Fatalf("read error = %v, want the idle timeout", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("idle timeout must not look like a client cancellation")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stalled stream ended after %v, want about the 100ms timeout", elapsed)
	}
	if string(got) != "data: {}\n\n" {
		t.Fatalf("bytes before the stall = %q", got)
	}
}

func TestStreamCompleteTLSHandshakeBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, c) }()
		}
	}()

	p := NewHTTP("mock", "https://"+ln.Addr().String(), 50*time.Millisecond, discardLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err = p.StreamComplete(ctx, chatReq())
	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
		t.Fatalf("err = %v, want %s", err, core.CodeUpstream)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("TLS handshake against a silent server took %v, want about the 50ms timeout", elapsed)
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

func statusUpstream(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

func TestCompleteClientErrorsAreInvalidRequest(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity} {
		upstream := statusUpstream(status, `{"error":{"message":"secret upstream detail"}}`)
		p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
		_, err := p.Complete(context.Background(), chatReq())
		upstream.Close()

		var norm *core.Error
		if !errors.As(err, &norm) || norm.Code != core.CodeInvalidRequest {
			t.Fatalf("status %d: err = %v, want %s", status, err, core.CodeInvalidRequest)
		}
		if strings.Contains(norm.Message, "secret upstream detail") {
			t.Fatalf("status %d: upstream body leaked into %q", status, norm.Message)
		}
	}
}

func TestCompleteRetryableStatusesAreUpstreamErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		upstream := statusUpstream(status, `{}`)
		p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
		_, err := p.Complete(context.Background(), chatReq())
		upstream.Close()

		var norm *core.Error
		if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
			t.Fatalf("status %d: err = %v, want %s", status, err, core.CodeUpstream)
		}
	}
}

func TestStreamComplete400IsInvalidRequest(t *testing.T) {
	upstream := statusUpstream(http.StatusBadRequest, `{"error":{"message":"bad"}}`)
	defer upstream.Close()

	p := NewHTTP("mock", upstream.URL, time.Second, discardLogger())
	_, err := p.StreamComplete(context.Background(), chatReq())
	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeInvalidRequest {
		t.Fatalf("err = %v, want %s", err, core.CodeInvalidRequest)
	}
}

func TestComplete200WithoutCompletionIsUpstreamError(t *testing.T) {
	bodies := map[string]string{
		"error object":   `{"error":{"message":"quota exceeded"}}`,
		"no choices":     `{"id":"cmpl-1","choices":[]}`,
		"oversized body": `{"choices":[{"message":{"content":"` + strings.Repeat("x", maxUpstreamBody) + `"}}]}`,
	}
	for name, body := range bodies {
		upstream := statusUpstream(http.StatusOK, body)
		p := NewHTTP("mock", upstream.URL, 5*time.Second, discardLogger())
		_, err := p.Complete(context.Background(), chatReq())
		upstream.Close()

		var norm *core.Error
		if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
			t.Fatalf("%s: err = %v, want %s", name, err, core.CodeUpstream)
		}
	}
}
