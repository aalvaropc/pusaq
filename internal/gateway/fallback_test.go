package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

// scriptedProvider fails or serves per configuration and counts calls.
type scriptedProvider struct {
	name  string
	fail  bool
	calls int
	sse   string
}

func (s *scriptedProvider) Name() string { return s.name }

func (s *scriptedProvider) Complete(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.calls++
	if s.fail {
		return provider.ChatResponse{}, core.NewError(core.CodeUpstream, "upstream provider returned status 500")
	}
	return provider.ChatResponse{
		ID:    "cmpl-" + s.name,
		Model: req.Model,
		Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "from " + s.name},
			FinishReason: "stop",
		}},
		Usage: core.Usage{PromptTokens: 3, CompletionTokens: 2},
	}, nil
}

func (s *scriptedProvider) StreamComplete(_ context.Context, _ provider.ChatRequest) (io.ReadCloser, error) {
	s.calls++
	if s.fail {
		return nil, core.NewError(core.CodeUpstream, "upstream provider unavailable")
	}
	return io.NopCloser(strings.NewReader(s.sse)), nil
}

// fallbackRoutes maps pusaq/fast to primary with backup as fallback.
func fallbackRoutes() map[string]Route {
	return map[string]Route{
		"pusaq/fast": {Provider: "primary", Model: "mock-model", Fallbacks: []string{"backup"}},
	}
}

// tightBreakers builds a registry whose breakers open fast (min 2 samples).
func tightBreakers(t *testing.T) (BreakerFor, map[string]*breaker.Breaker) {
	t.Helper()
	m := map[string]*breaker.Breaker{
		"primary": breaker.New(breaker.Config{Window: 4, MinSamples: 2, FailureThreshold: 0.5, Cooldown: time.Hour, Probes: 1}),
		"backup":  breaker.New(breaker.Config{Window: 4, MinSamples: 2, FailureThreshold: 0.5, Cooldown: time.Hour, Probes: 1}),
	}
	return func(b string) *breaker.Breaker { return m[b] }, m
}

func fallbackServer(t *testing.T, primary, backup provider.Provider, breakers BreakerFor) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), Auth(nil))
	completions := CompletionsHandler(fallbackRoutes(),
		map[string]provider.Provider{"primary": primary, "backup": backup},
		CompletionsOptions{Breakers: breakers, Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions), &events
}

func TestFallbackServesWhenPrimaryFails(t *testing.T) {
	primary := &scriptedProvider{name: "primary", fail: true}
	backup := &scriptedProvider{name: "backup"}
	brs, _ := tightBreakers(t)
	s, events := fallbackServer(t, primary, backup, brs)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 via fallback", rec.Code)
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("calls primary=%d backup=%d, want 1/1", primary.calls, backup.calls)
	}
	e := (*events)[0]
	if e.ServedProvider != "backup" || e.Attempts != 2 {
		t.Fatalf("event = served_provider %q attempts %d, want backup/2", e.ServedProvider, e.Attempts)
	}
}

func TestOpenBreakerSkipsPrimaryWithoutCalling(t *testing.T) {
	primary := &scriptedProvider{name: "primary", fail: true}
	backup := &scriptedProvider{name: "backup"}
	brs, m := tightBreakers(t)
	// Open the primary's breaker directly.
	for range 2 {
		if m["primary"].Allow() {
			m["primary"].Record(false)
		}
	}
	if m["primary"].State() != breaker.StateOpen {
		t.Fatal("primary breaker not open")
	}
	s, events := fallbackServer(t, primary, backup, brs)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if primary.calls != 0 {
		t.Fatalf("primary called %d times through an open breaker", primary.calls)
	}
	e := (*events)[0]
	if e.ServedProvider != "backup" || e.Attempts != 1 {
		t.Fatalf("event = %q/%d, want backup with a single attempt (fail-fast)", e.ServedProvider, e.Attempts)
	}
}

func TestAllBackendsDownIs502WithAttempts(t *testing.T) {
	primary := &scriptedProvider{name: "primary", fail: true}
	backup := &scriptedProvider{name: "backup", fail: true}
	brs, _ := tightBreakers(t)
	s, events := fallbackServer(t, primary, backup, brs)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	e := (*events)[0]
	if e.Attempts != 2 || e.ErrorCode != core.CodeUpstream {
		t.Fatalf("event = %+v, want 2 attempts and upstream_error", e)
	}
}

func TestRepeatedFailuresOpenBreakerAndStopPayingTimeouts(t *testing.T) {
	primary := &scriptedProvider{name: "primary", fail: true}
	backup := &scriptedProvider{name: "backup"}
	brs, m := tightBreakers(t)
	s, _ := fallbackServer(t, primary, backup, brs)

	for range 4 {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want every request served via fallback", rec.Code)
		}
	}
	// Two failures (min samples) opened the breaker; subsequent requests skip.
	if m["primary"].State() != breaker.StateOpen {
		t.Fatal("primary breaker never opened under sustained failures")
	}
	if primary.calls != 2 {
		t.Fatalf("primary paid %d attempts, want exactly min-samples 2 before fail-fast", primary.calls)
	}
}

func TestStreamFallsBackBeforeFirstByte(t *testing.T) {
	primary := &scriptedProvider{name: "primary", fail: true}
	backup := &scriptedProvider{name: "backup", sse: fakeSSE}
	brs, _ := tightBreakers(t)
	s, events := fallbackServer(t, primary, backup, brs)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want streamed 200 via fallback", rec.Code)
	}
	if !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") {
		t.Fatal("fallback stream incomplete")
	}
	e := (*events)[0]
	if e.ServedProvider != "backup" || e.Attempts != 2 {
		t.Fatalf("event = %q/%d", e.ServedProvider, e.Attempts)
	}
}

func TestMidStreamFailureDoesNotRetry(t *testing.T) {
	primary := &streamQueue{streams: []io.ReadCloser{brokenStream()}}
	backup := &scriptedProvider{name: "backup", sse: fakeSSE}
	brs, _ := tightBreakers(t)
	s, events := fallbackServer(t, primary, backup, brs)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))

	if backup.calls != 0 {
		t.Fatal("backup tried after the stream had begun")
	}
	if body := rec.Body.String(); strings.Contains(body, "[DONE]") || !strings.HasSuffix(body, streamErrorEvent) {
		t.Fatalf("body = %q, want the partial stream and an error event", body)
	}
	assertErrorEvent(t, rec.Body.String())
	e := (*events)[0]
	if e.ErrorCode != core.CodeUpstream || e.ServedProvider != "primary" {
		t.Fatalf("event = %+v, want mid-stream upstream_error on primary", e)
	}
}

func TestBrokenStreamsOpenTheBreaker(t *testing.T) {
	primary := &streamQueue{streams: []io.ReadCloser{brokenStream(), brokenStream()}}
	backup := &scriptedProvider{name: "backup", sse: fakeSSE}
	brs, m := tightBreakers(t)
	s, events := fallbackServer(t, primary, backup, brs)

	for range 3 {
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))
	}

	if got := m["primary"].State(); got != breaker.StateOpen {
		t.Fatalf("primary breaker = %s, want open after two broken streams", got)
	}
	if primary.calls != 2 || (*events)[2].ServedProvider != "backup" {
		t.Fatalf("primary calls = %d, third served by %q; want the open breaker to route to backup", primary.calls, (*events)[2].ServedProvider)
	}
}

func TestStartedStreamOutcomeFeedsBreaker(t *testing.T) {
	for _, tc := range []struct {
		name      string
		readFully bool
		want      breaker.State
	}{
		{"completed streams count as successes", true, breaker.StateClosed},
		{"client disconnects count as nothing", false, breaker.StateOpen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := &streamQueue{streams: []io.ReadCloser{
				io.NopCloser(strings.NewReader(fakeSSE)), io.NopCloser(strings.NewReader(fakeSSE)),
				brokenStream(), brokenStream(),
			}}
			brs, m := tightBreakers(t)
			h := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID())(CompletionsHandler(fallbackRoutes(),
				map[string]provider.Provider{"primary": primary, "backup": &scriptedProvider{name: "backup"}},
				CompletionsOptions{Breakers: brs, Logger: discardLogger()}))

			for i := range 4 {
				resp, err := h(context.Background(), &core.Request{Op: "chat.completions", Payload: streamingPayload()})
				if err != nil {
					t.Fatalf("request %d: %v", i, err)
				}
				if tc.readFully || i >= 2 {
					_, _ = io.ReadAll(resp.Stream)
				}
				_ = resp.Stream.Close()
			}

			if got := m["primary"].State(); got != tc.want {
				t.Fatalf("primary breaker = %s, want %s", got, tc.want)
			}
		})
	}
}

type streamQueue struct {
	streams []io.ReadCloser
	calls   int
}

func (q *streamQueue) Name() string { return "primary" }

func (q *streamQueue) Complete(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, core.NewError(core.CodeUpstream, "buffered not used here")
}

func (q *streamQueue) StreamComplete(_ context.Context, _ provider.ChatRequest) (io.ReadCloser, error) {
	s := q.streams[q.calls]
	q.calls++
	return s, nil
}

func brokenStream() io.ReadCloser {
	return &errReader{r: strings.NewReader("data: partial\n\n"), err: errors.New("connection reset")}
}

func TestValidationErrorsDoNotConsumeAttempts(t *testing.T) {
	primary := &scriptedProvider{name: "primary"}
	backup := &scriptedProvider{name: "backup"}
	brs, _ := tightBreakers(t)
	s, events := fallbackServer(t, primary, backup, brs)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if primary.calls != 0 || backup.calls != 0 {
		t.Fatal("backends called for a validation failure")
	}
	if (*events)[0].Attempts != 0 {
		t.Fatalf("attempts = %d, want 0", (*events)[0].Attempts)
	}
}

func TestUpstreamClientErrorStopsChainWithoutTrippingBreaker(t *testing.T) {
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"max_tokens is invalid"}}`)
	}))
	defer rejecting.Close()
	backup := &scriptedProvider{name: "backup"}
	brs, m := tightBreakers(t)
	primary := provider.NewHTTP("primary", rejecting.URL, time.Second, discardLogger())
	s, events := fallbackServer(t, primary, backup, brs)

	for i := range 4 {
		if rec := post(s, string(validPayload())); rec.Code != http.StatusBadRequest {
			t.Fatalf("request %d: status = %d, want 400", i, rec.Code)
		}
	}
	if backup.calls != 0 {
		t.Fatalf("backup called %d times for a request the primary rejected", backup.calls)
	}
	for name, br := range m {
		if got := br.State(); got != breaker.StateClosed {
			t.Fatalf("%s breaker = %s, want closed: a rejected request is not a backend failure", name, got)
		}
	}
	if code := (*events)[0].ErrorCode; code != core.CodeInvalidRequest {
		t.Fatalf("event code = %s, want %s", code, core.CodeInvalidRequest)
	}
}

type blockingProvider struct {
	name  string
	calls int
}

func (b *blockingProvider) Name() string { return b.name }

func (b *blockingProvider) Complete(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	b.calls++
	<-ctx.Done()
	return provider.ChatResponse{}, &core.Error{Code: core.CodeUpstream, Message: "upstream provider unavailable", Err: ctx.Err()}
}

func (b *blockingProvider) StreamComplete(ctx context.Context, _ provider.ChatRequest) (io.ReadCloser, error) {
	b.calls++
	<-ctx.Done()
	return nil, &core.Error{Code: core.CodeUpstream, Message: "upstream provider unavailable", Err: ctx.Err()}
}

func TestCanceledRequestStopsChainWithoutBreakerFailure(t *testing.T) {
	payloads := map[string][]byte{"buffered": validPayload(), "stream": streamingPayload()}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			primary := &blockingProvider{name: "primary"}
			backup := &scriptedProvider{name: "backup", sse: fakeSSE}
			brs, m := tightBreakers(t)
			h := CompletionsHandler(fallbackRoutes(),
				map[string]provider.Provider{"primary": primary, "backup": backup},
				CompletionsOptions{Breakers: brs, Logger: discardLogger()})

			for range 2 {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(20*time.Millisecond, cancel)
				_, err := h(ctx, &core.Request{Op: "chat.completions", Payload: payload})
				cancel()
				if code := core.AsError(err).Code; code != core.CodeClientCanceled {
					t.Fatalf("err = %v, want %s", err, core.CodeClientCanceled)
				}
			}
			if backup.calls != 0 {
				t.Fatalf("backup called %d times after the client left", backup.calls)
			}
			if got := m["primary"].State(); got != breaker.StateClosed {
				t.Fatalf("primary breaker = %s, want closed: cancellations are not failures", got)
			}
		})
	}
}
