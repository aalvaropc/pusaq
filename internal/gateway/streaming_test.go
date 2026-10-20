package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/mockprovider"
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

func drain(r io.Reader) (string, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if errors.Is(err, io.EOF) {
			return string(out), nil
		}
		if err != nil {
			return string(out), err
		}
	}
}

func assertErrorEvent(t *testing.T, body string) {
	t.Helper()
	events := strings.Split(strings.TrimSpace(body), "\n\n")
	var e errorBody
	if err := json.Unmarshal([]byte(strings.TrimPrefix(events[len(events)-1], "data: ")), &e); err != nil || e.Error.Code != core.CodeUpstream {
		t.Fatalf("last event = %q, want an %s error event", events[len(events)-1], core.CodeUpstream)
	}
}

func TestUsageScannerParsesEventFraming(t *testing.T) {
	const usage = `{"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`
	streams := map[string]string{
		"LF":                      fakeSSE,
		"CRLF":                    "data: " + usage + "\r\n\r\ndata: [DONE]\r\n\r\n",
		"CR":                      "data: " + usage + "\r\rdata: [DONE]\r\r",
		"multi-line data":         "data: {\"choices\":[],\ndata: \"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n",
		"no space after colon":    "data:" + usage + "\n\ndata:[DONE]\n\n",
		"comments and fields":     ": ping\n\nevent: chunk\nid: 1\ndata: " + usage + "\n\ndata: [DONE]\n\n",
		"DONE without newline":    "data: " + usage + "\n\ndata: [DONE]",
		"DONE without blank line": "data: " + usage + "\n\ndata: [DONE]\n",
	}
	readers := map[string]func(string) io.Reader{
		"whole":    func(s string) io.Reader { return strings.NewReader(s) },
		"one byte": func(s string) io.Reader { return iotest.OneByteReader(strings.NewReader(s)) },
	}
	for name, stream := range streams {
		for mode, reader := range readers {
			t.Run(name+"/"+mode, func(t *testing.T) {
				req := &core.Request{}
				scanner := &usageScanningStream{inner: io.NopCloser(reader(stream)), req: req}
				out, err := drain(scanner)
				if err != nil || out != stream {
					t.Fatalf("drain() = %q, %v; want the stream untouched", out, err)
				}
				if req.Usage != (core.Usage{PromptTokens: 9, CompletionTokens: 4}) {
					t.Fatalf("usage = %+v, want 9/4", req.Usage)
				}
				if !scanner.completed() || scanner.failed {
					t.Fatalf("completed = %v, failed = %v", scanner.completed(), scanner.failed)
				}
			})
		}
	}
}

func TestUsageScannerEndsUnterminatedStreamWithErrorEvent(t *testing.T) {
	unterminated := strings.TrimSuffix(fakeSSE, "data: [DONE]\n\n")
	req := &core.Request{}
	scanner := &usageScanningStream{inner: io.NopCloser(strings.NewReader(unterminated)), req: req}

	out, err := drain(scanner)

	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
	if out != unterminated+streamErrorEvent {
		t.Fatalf("out = %q, want the stream followed by one error event", out)
	}
	assertErrorEvent(t, out)
	if req.Usage.PromptTokens != 9 || scanner.completed() || !scanner.failed {
		t.Fatalf("usage = %+v, completed = %v, failed = %v", req.Usage, scanner.completed(), scanner.failed)
	}
}

func TestUsageScannerBoundsEventSize(t *testing.T) {
	huge := "data: " + strings.Repeat("x", 2*maxEventBytes)
	scanner := &usageScanningStream{inner: io.NopCloser(strings.NewReader(huge)), req: &core.Request{}}

	out, err := drain(scanner)

	if !errors.Is(err, errEventTooLarge) || !scanner.failed {
		t.Fatalf("err = %v, failed = %v; want errEventTooLarge", err, scanner.failed)
	}
	if !strings.HasSuffix(out, streamErrorEvent) || len(out) > maxEventBytes+4096+len(streamErrorEvent) {
		t.Fatalf("forwarded %d bytes, want the read stopped near %d with an error event", len(out), maxEventBytes)
	}
}

func TestUsageScannerPassesClientCancellationThrough(t *testing.T) {
	inner := &errReader{r: strings.NewReader("data: partial\n\n"), err: context.Canceled}
	scanner := &usageScanningStream{inner: inner, req: &core.Request{}}

	out, err := drain(scanner)

	if !errors.Is(err, context.Canceled) || out != "data: partial\n\n" || scanner.failed {
		t.Fatalf("drain() = %q, %v, failed = %v; want the cancellation without an error event", out, err, scanner.failed)
	}
}

func TestUsageScannerEndsCleanlyAfterDone(t *testing.T) {
	inner := &errReader{r: strings.NewReader(fakeSSE), err: errors.New("connection reset")}
	scanner := &usageScanningStream{inner: inner, req: &core.Request{}}

	out, err := drain(scanner)

	if err != nil || out != fakeSSE || !scanner.completed() || scanner.failed {
		t.Fatalf("drain() = %q, %v; want a clean end after [DONE]", out, err)
	}
}

func TestStreamWithoutDoneEndsAsUpstreamError(t *testing.T) {
	p := okProvider()
	p.sse = strings.TrimSuffix(fakeSSE, "data: [DONE]\n\n")
	shadow := &recordingShadow{}
	var events []core.CostEvent
	pipeline := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, discardLogger()), RequestID())
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Shadow: shadow, Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, completions)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))

	if body := rec.Body.String(); body != p.sse+streamErrorEvent {
		t.Fatalf("body = %q, want the provider bytes followed by one error event", body)
	}
	if len(events) != 1 || events[0].ErrorCode != core.CodeUpstream {
		t.Fatalf("events = %+v, want one %s", events, core.CodeUpstream)
	}
	if got := shadow.all(); len(got) != 0 {
		t.Fatalf("shadow offered %d samples for an unterminated stream", len(got))
	}
}

func TestStreamUsageFromAnOpenAICompatibleUpstream(t *testing.T) {
	mock, err := mockprovider.NewServer(discardLogger(), mockprovider.Options{StreamChunkDelay: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(mock)
	t.Cleanup(upstream.Close)
	tokens := &fakeTokenLimiter{allowed: true}
	var events []core.CostEvent
	pipeline := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, discardLogger()), RequestID())
	completions := CompletionsHandler(testRoutes(),
		map[string]provider.Provider{"mock": provider.NewHTTP("mock", upstream.URL+"/v1", time.Second, discardLogger())},
		CompletionsOptions{Tokens: tokens, ReserveDefault: 100, Prices: testPrices(), Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, completions)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))

	if rec.Code != http.StatusOK || !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	e := events[0]
	if e.Outcome != core.OutcomeSuccess || e.Usage != (core.Usage{PromptTokens: 12, CompletionTokens: 11}) {
		t.Fatalf("event = %+v, want success with the provider's 12/11 usage", e)
	}
	if want := (core.Cost{USD: 5.6e-6, TableVersion: "v-test"}); e.Cost != want {
		t.Fatalf("cost = %+v, want %+v", e.Cost, want)
	}
	if len(tokens.adjusts) != 1 || tokens.adjusts[0] != 103-23 {
		t.Fatalf("adjusts = %v, want one refund of 80 (103 charged, 23 used)", tokens.adjusts)
	}
}

func TestStreamingEndToEndThroughServer(t *testing.T) {
	p := okProvider()
	p.sse = fakeSSE
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID())
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p}, CompletionsOptions{Logger: discardLogger()})
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
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p}, CompletionsOptions{Logger: discardLogger()})
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
