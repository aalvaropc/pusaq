package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

// fakeSSE is a minimal upstream stream: two content chunks, a usage chunk,
// and the DONE terminator.
const fakeSSE = "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n" +
	"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4}}\n\n" +
	"data: [DONE]\n\n"

func streamingPayload() []byte {
	return []byte(`{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}],"stream":true}`)
}

// errReader fails after serving its content, simulating an upstream drop.
type errReader struct {
	r    io.Reader
	err  error
	done bool
}

func (e *errReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		e.done = true
		return n, e.err
	}
	return n, err
}

func (e *errReader) Close() error { return nil }

func TestStreamEventEmittedAtEOFNotAtReturn(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	terminal := func(_ context.Context, req *core.Request) (*core.Response, error) {
		return &core.Response{Status: http.StatusOK, Stream: io.NopCloser(strings.NewReader("abc"))}, nil
	}
	resp, err := core.Chain(Accounting(emit, discardLogger()), RequestID())(terminal)(context.Background(), &core.Request{Op: "t"})
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}

	if len(events) != 0 {
		t.Fatalf("event emitted before the stream was consumed")
	}
	body, _ := io.ReadAll(resp.Stream)
	_ = resp.Stream.Close()

	if string(body) != "abc" {
		t.Fatalf("stream body = %q", body)
	}
	if len(events) != 1 || events[0].Outcome != core.OutcomeSuccess {
		t.Fatalf("events = %+v, want one success at EOF", events)
	}
}

func TestStreamCloseBeforeEOFIsInterrupted(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	terminal := func(_ context.Context, _ *core.Request) (*core.Response, error) {
		return &core.Response{Status: http.StatusOK, Stream: io.NopCloser(strings.NewReader("never fully read"))}, nil
	}
	resp, err := core.Chain(Accounting(emit, discardLogger()), RequestID())(terminal)(context.Background(), &core.Request{Op: "t"})
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}
	_ = resp.Stream.Close() // client walked away

	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(events))
	}
	e := events[0]
	if e.Outcome != core.OutcomeError || e.ErrorCode != core.CodeStreamInterrupted {
		t.Fatalf("event = %+v, want %s", e, core.CodeStreamInterrupted)
	}
}

func TestStreamReadErrorIsUpstreamError(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	broken := &errReader{r: strings.NewReader("partial"), err: errors.New("connection reset")}
	terminal := func(_ context.Context, _ *core.Request) (*core.Response, error) {
		return &core.Response{Status: http.StatusOK, Stream: broken}, nil
	}
	resp, err := core.Chain(Accounting(emit, discardLogger()), RequestID())(terminal)(context.Background(), &core.Request{Op: "t"})
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}
	_, _ = io.ReadAll(resp.Stream)
	_ = resp.Stream.Close()

	if len(events) != 1 {
		t.Fatalf("emitted %d events, want exactly 1", len(events))
	}
	e := events[0]
	if e.Outcome != core.OutcomeError || e.ErrorCode != core.CodeUpstream {
		t.Fatalf("event = %+v, want %s", e, core.CodeUpstream)
	}
}

func TestUsageScannerCapturesUsageAndPassesBytesThrough(t *testing.T) {
	req := &core.Request{}
	scanner := &usageScanningStream{inner: io.NopCloser(strings.NewReader(fakeSSE)), req: req}

	// Read in tiny chunks to exercise line reassembly across reads.
	var out []byte
	buf := make([]byte, 7)
	for {
		n, err := scanner.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}

	if string(out) != fakeSSE {
		t.Fatal("scanner altered the passthrough bytes")
	}
	if req.Usage.PromptTokens != 9 || req.Usage.CompletionTokens != 4 {
		t.Fatalf("usage = %+v, want 9/4", req.Usage)
	}
}

func TestStreamingEndToEndThroughServer(t *testing.T) {
	p := okProvider()
	p.sse = fakeSSE
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID())
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p})
	s := NewServer(discardLogger(), pipeline, completions)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if got := rec.Header().Get(ServedModelHeader); got != "mock-model" {
		t.Fatalf("%s = %q", ServedModelHeader, got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"Hello `) || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("body = %q", body)
	}
	if len(events) != 1 {
		t.Fatalf("emitted %d cost events, want exactly 1", len(events))
	}
	e := events[0]
	if e.Outcome != core.OutcomeSuccess || e.Usage.PromptTokens != 9 || e.Usage.CompletionTokens != 4 {
		t.Fatalf("event = %+v", e)
	}
}

func TestStreamingUnknownModelStaysNormalizedError(t *testing.T) {
	p := okProvider()
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p})
	_, err := h(context.Background(), &core.Request{
		Payload: []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"x"}],"stream":true}`),
	})
	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeInvalidRequest {
		t.Fatalf("err = %v, want %s", err, core.CodeInvalidRequest)
	}
}

func TestStreamContextCanceledIsInterrupted(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	canceled := &errReader{r: strings.NewReader("partial"), err: context.Canceled}
	terminal := func(_ context.Context, _ *core.Request) (*core.Response, error) {
		return &core.Response{Status: http.StatusOK, Stream: canceled}, nil
	}
	resp, err := core.Chain(Accounting(emit, discardLogger()), RequestID())(terminal)(context.Background(), &core.Request{Op: "t"})
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}
	_, _ = io.ReadAll(resp.Stream)
	_ = resp.Stream.Close()

	if len(events) != 1 || events[0].ErrorCode != core.CodeStreamInterrupted {
		t.Fatalf("events = %+v, want one %s", events, core.CodeStreamInterrupted)
	}
}
