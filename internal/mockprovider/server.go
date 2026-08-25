// Package mockprovider implements Pusaq's fake upstream model provider.
// Rate-limiting and failover experiments run against it at zero token cost
// (design.md §6): it returns a canned OpenAI-shaped chat completion with
// configurable latency and error rate.
package mockprovider

import (
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
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
}

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
	return nil
}

// cannedResponse is an OpenAI-shaped chat completion, so the gateway's
// upcoming chat-completions slice can proxy to the mock unchanged.
const cannedResponse = `{
  "id": "mock-cmpl-1",
  "object": "chat.completion",
  "model": "mock-model",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "This is a canned response from the Pusaq mock provider."},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 12, "completion_tokens": 11, "total_tokens": 23}
}`

// Server is the mock provider's HTTP handler.
type Server struct {
	opts   Options
	logger *slog.Logger
	mux    *http.ServeMux
	// sleep is swapped in tests to observe sampled delays without waiting.
	sleep func(time.Duration)
	// random returns a float in [0,1); swapped in tests for determinism.
	random func() float64
}

// NewServer builds the mock with validated options.
func NewServer(logger *slog.Logger, opts Options) (*Server, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	s := &Server{
		opts:   opts,
		logger: logger,
		mux:    http.NewServeMux(),
		sleep:  time.Sleep,
		random: rand.Float64,
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleCompletion)
	return s, nil
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

func (s *Server) handleCompletion(w http.ResponseWriter, _ *http.Request) {
	s.sleep(s.sampleLatency())

	if s.random() < s.opts.ErrorRate {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"mock_upstream_error","message":"injected failure"}}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, cannedResponse)
}
