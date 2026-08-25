// Package gateway is Pusaq's HTTP layer: it adapts HTTP to the
// transport-agnostic core pipeline, renders normalized errors in one JSON
// shape, and owns the operational behavior (liveness, readiness, draining).
package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

// maxBodyBytes bounds request bodies until per-tenant policy limits exist.
const maxBodyBytes = 1 << 20 // 1 MiB

// Server wires the pipeline into an http.Handler and controls readiness.
type Server struct {
	logger   *slog.Logger
	pipeline core.Stage
	ready    atomic.Bool
	mux      *http.ServeMux
}

// NewServer builds the gateway with the given composed pipeline stage.
// The server starts not-ready; call SetReady(true) once serving begins.
func NewServer(logger *slog.Logger, pipeline core.Stage) *Server {
	s := &Server{logger: logger, pipeline: pipeline, mux: http.NewServeMux()}

	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	// Temporary smoke endpoint: proves a request traverses the pipeline and
	// always emits a cost event. Removed when chat completions lands.
	s.mux.Handle("POST /v1/echo", s.pipelineHandler("echo", echoHandler))

	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// SetReady flips the readiness flag: true once serving begins, false when
// draining starts (and, later, when failure-injection experiments demand it).
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "draining\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ready\n")
}

// pipelineHandler adapts HTTP to the core pipeline for one operation: it
// builds the domain envelope, runs the composed stages around the terminal
// handler, stamps the request-ID header on every outcome, and renders
// normalized errors.
func (s *Server) pipelineHandler(op string, terminal core.Handler) http.Handler {
	h := s.pipeline(terminal)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil {
			s.writeError(w, "", core.NewError(core.CodeInvalidRequest, "failed to read request body"))
			return
		}
		if len(body) > maxBodyBytes {
			s.writeError(w, "", core.NewError(core.CodeInvalidRequest, "request body too large"))
			return
		}

		req := &core.Request{ArrivedAt: time.Now().UTC(), Op: op, Payload: body}
		resp, err := h(r.Context(), req)
		if err != nil {
			s.writeError(w, req.ID, core.AsError(err))
			return
		}

		header := w.Header()
		for k, v := range resp.Header {
			header.Set(k, v)
		}
		header.Set(RequestIDHeader, req.ID)
		if header.Get("Content-Type") == "" {
			header.Set("Content-Type", "application/json")
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(resp.Body)
	})
}

// errorBody is the single JSON error shape every gateway failure uses.
type errorBody struct {
	Error struct {
		Code    core.Code `json:"code"`
		Message string    `json:"message"`
	} `json:"error"`
}

func (s *Server) writeError(w http.ResponseWriter, requestID string, e *core.Error) {
	var body errorBody
	body.Error.Code = e.Code
	body.Error.Message = e.Message

	w.Header().Set("Content-Type", "application/json")
	if requestID != "" {
		w.Header().Set(RequestIDHeader, requestID)
	}
	w.WriteHeader(e.Status())
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.Error("failed to encode error response", slog.Any("error", err))
	}
}

// echoHandler is the terminal handler of the smoke endpoint: it returns the
// request payload unchanged.
func echoHandler(_ context.Context, req *core.Request) (*core.Response, error) {
	return &core.Response{
		Status: http.StatusOK,
		Body:   req.Payload,
	}, nil
}
