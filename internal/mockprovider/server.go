// Package mockprovider implements Pusaq's fake upstream model provider.
// Rate-limiting and failover experiments run against it at zero token cost
// (docs/design.md §6): it returns a canned OpenAI-shaped chat completion with
// configurable latency and error rate.
package mockprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configure the mock's behavior.
type Options struct {
	// LatencyP50 and LatencyP99 shape the response-latency distribution:
	// 90% of responses sample uniformly around P50, 10% stretch toward P99.
	LatencyP50 time.Duration
	LatencyP99 time.Duration
	// ErrorRate is the probability [0,1] of responding 500 instead of 200.
	ErrorRate float64
	// StreamChunkDelay spaces the chunks of a streamed completion, so a
	// stream can be made to last long enough to be in flight when an
	// experiment injects a failure. Zero means the default.
	StreamChunkDelay time.Duration
}

// defaultStreamChunkDelay keeps streaming observable without making local
// runs slow.
const defaultStreamChunkDelay = 10 * time.Millisecond

// Validate rejects meaningless configurations.
func (o Options) Validate() error {
	if o.LatencyP50 < 0 || o.LatencyP99 < 0 {
		return fmt.Errorf("latencies must be non-negative")
	}
	if o.LatencyP99 < o.LatencyP50 {
		return fmt.Errorf("latency-p99 (%s) must be >= latency-p50 (%s)", o.LatencyP99, o.LatencyP50)
	}
	if o.ErrorRate < 0 || o.ErrorRate > 1 {
		return fmt.Errorf("error-rate must be in [0,1], got %g", o.ErrorRate)
	}
	if o.StreamChunkDelay < 0 {
		return fmt.Errorf("stream-chunk-delay must be non-negative, got %s", o.StreamChunkDelay)
	}
	return nil
}

// Stats is the upstream-side ground truth a load run checks against: how
// many completion requests reached the mock, how many it failed on purpose,
// how many arrived per requested model, and whether a failure window is
// open right now.
type Stats struct {
	Requests uint64            `json:"requests"`
	Errors   uint64            `json:"errors"`
	ByModel  map[string]uint64 `json:"by_model"`
	Failing  bool              `json:"failing"`
}

// Server is the mock provider's HTTP handler.
type Server struct {
	opts   Options
	logger *slog.Logger
	mux    *http.ServeMux
	// sleep is swapped in tests to observe sampled delays without waiting.
	sleep func(time.Duration)
	// random returns a float in [0,1); swapped in tests for determinism.
	random func() float64
	// now is the clock the failure window is measured against; swapped in
	// tests.
	now func() time.Time
	// failUntil is the end of the current failure window in Unix
	// nanoseconds; zero means none.
	failUntil atomic.Int64

	requests  atomic.Uint64
	errors    atomic.Uint64
	byModelMu sync.Mutex
	byModel   map[string]uint64
}

// NewServer builds the mock with validated options.
func NewServer(logger *slog.Logger, opts Options) (*Server, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if opts.StreamChunkDelay == 0 {
		opts.StreamChunkDelay = defaultStreamChunkDelay
	}
	s := &Server{
		opts:    opts,
		logger:  logger,
		mux:     http.NewServeMux(),
		sleep:   time.Sleep,
		random:  rand.Float64,
		now:     time.Now,
		byModel: make(map[string]uint64),
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	s.mux.HandleFunc("GET /stats", s.handleStats)
	s.mux.HandleFunc("POST /admin/fail", s.handleFailStart)
	s.mux.HandleFunc("DELETE /admin/fail", s.handleFailClear)
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleCompletion)
	return s, nil
}

// failing reports whether the mock is inside a failure window: every
// completion returns 500 until it ends. It is how chaos runs make the
// upstream die at a known instant (docs/design.md §8).
func (s *Server) failing() bool {
	until := s.failUntil.Load()
	return until != 0 && s.now().UnixNano() < until
}

// handleFailStart opens a failure window of ?for=<duration>, replacing any
// current one. Admin endpoints exist for the mock only; it is never
// published.
func (s *Server) handleFailStart(w http.ResponseWriter, r *http.Request) {
	d, err := time.ParseDuration(r.URL.Query().Get("for"))
	if err != nil || d <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"for must be a positive duration"}`)
		return
	}
	from := s.now()
	until := from.Add(d)
	s.failUntil.Store(until.UnixNano())
	s.logger.Info("failure_window",
		slog.String("action", "start"),
		slog.Duration("for", d),
		slog.String("from", from.UTC().Format(time.RFC3339Nano)),
		slog.String("until", until.UTC().Format(time.RFC3339Nano)),
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"failing": true,
		"from":    from.UTC().Format(time.RFC3339Nano),
		"until":   until.UTC().Format(time.RFC3339Nano),
	})
}

// handleFailClear ends the failure window early.
func (s *Server) handleFailClear(w http.ResponseWriter, _ *http.Request) {
	s.failUntil.Store(0)
	s.logger.Info("failure_window",
		slog.String("action", "clear"),
		slog.String("at", s.now().UTC().Format(time.RFC3339Nano)),
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"failing":false}`+"\n")
}

// Stats returns a snapshot of the request counters.
func (s *Server) Stats() Stats {
	s.byModelMu.Lock()
	byModel := make(map[string]uint64, len(s.byModel))
	for k, v := range s.byModel {
		byModel[k] = v
	}
	s.byModelMu.Unlock()
	return Stats{
		Requests: s.requests.Load(),
		Errors:   s.errors.Load(),
		ByModel:  byModel,
		Failing:  s.failing(),
	}
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(s.Stats())
}

// record counts a completion request as received, keyed by the model it
// asked for. Counting happens before latency and error injection: a request
// that reached the mock was admitted by the gateway, whatever the mock does
// with it afterwards.
func (s *Server) record(model string) {
	s.requests.Add(1)
	s.byModelMu.Lock()
	s.byModel[model]++
	s.byModelMu.Unlock()
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// sampleLatency draws from the two-point latency model: most responses live
// around P50, a 10% tail stretches from P50 toward P99.
func (s *Server) sampleLatency() time.Duration {
	if s.opts.LatencyP99 == 0 {
		return 0
	}
	if s.random() < 0.9 {
		// Uniform in [0.5×P50, 1.5×P50], capped at P99.
		d := time.Duration(float64(s.opts.LatencyP50) * (0.5 + s.random()))
		return min(d, s.opts.LatencyP99)
	}
	// Tail: uniform in [P50, P99].
	spread := s.opts.LatencyP99 - s.opts.LatencyP50
	return s.opts.LatencyP50 + time.Duration(s.random()*float64(spread))
}

func (s *Server) handleCompletion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = json.Unmarshal(body, &req) // malformed body → buffered response
	if req.Model == "" {
		req.Model = "mock-model"
	}
	s.record(req.Model)

	s.sleep(s.sampleLatency())

	if s.failing() || s.random() < s.opts.ErrorRate {
		s.errors.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"mock_upstream_error","message":"injected failure"}}`)
		return
	}

	if req.Stream {
		s.streamCompletion(w, req.Model, req.StreamOptions.IncludeUsage)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp, _ := json.Marshal(map[string]any{
		"id":     "mock-cmpl-1",
		"object": "chat.completion",
		"model":  req.Model, // echo, like a real upstream serving the request
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": cannedContent},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 11, "total_tokens": 23},
	})
	_, _ = w.Write(resp)
}

// cannedContent is what the streaming mode splits into word chunks.
const cannedContent = "This is a canned response from the Pusaq mock provider."

// streamCompletion emits the canned content as OpenAI chat.completion.chunk
// events, a final usage chunk when the request asked for one, then
// "data: [DONE]".
func (s *Server) streamCompletion(w http.ResponseWriter, model string, includeUsage bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)

	writeEvent := func(payload string) {
		_, _ = io.WriteString(w, "data: "+payload+"\n\n")
		_ = rc.Flush()
	}

	chunk := func(delta, finish string) string {
		b, _ := json.Marshal(map[string]any{
			"id":     "mock-cmpl-1",
			"object": "chat.completion.chunk",
			"model":  model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]string{"content": delta},
				"finish_reason": nilIfEmpty(finish),
			}},
		})
		return string(b)
	}

	words := strings.Fields(cannedContent)
	for i, word := range words {
		delta := word
		if i < len(words)-1 {
			delta += " "
		}
		finish := ""
		if i == len(words)-1 {
			finish = "stop"
		}
		writeEvent(chunk(delta, finish))
		s.sleep(s.opts.StreamChunkDelay)
	}

	if includeUsage {
		usage, _ := json.Marshal(map[string]any{
			"id":      "mock-cmpl-1",
			"object":  "chat.completion.chunk",
			"model":   model,
			"choices": []any{},
			"usage":   map[string]int{"prompt_tokens": 12, "completion_tokens": 11, "total_tokens": 23},
		})
		writeEvent(string(usage))
	}
	writeEvent("[DONE]")
}

// nilIfEmpty renders OpenAI's null finish_reason for non-final chunks.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
