// Package gateway is Pusaq's HTTP layer: it adapts HTTP to the
// transport-agnostic core pipeline, renders normalized errors in one JSON
// shape, and owns the operational behavior (liveness, readiness, draining).
package gateway

import (
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

// NewServer builds the gateway with the given composed pipeline stage and
// the terminal handler for chat completions. The server starts not-ready;
// call SetReady(true) once serving begins.
func NewServer(logger *slog.Logger, pipeline core.Stage, completions core.Handler) *Server {
	s := &Server{logger: logger, pipeline: pipeline, mux: http.NewServeMux()}

	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.Handle("POST /v1/chat/completions", s.pipelineHandler("chat.completions", completions))

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
			// After model resolution, even errors report the attempted model.
			if req.ServedModel != "" {
				w.Header().Set(ServedModelHeader, req.ServedModel)
			}
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

		if resp.Stream != nil {
			s.copyStream(w, resp.Stream)
			return
		}
		_, _ = w.Write(resp.Body)
	})
}

// copyStream forwards a streamed response with a flush per read so chunks
// reach the client as they arrive, and closes the stream — which finalizes
// the request's cost event (see finalizingStream).
func (s *Server) copyStream(w http.ResponseWriter, stream io.ReadCloser) {
	defer func() { _ = stream.Close() }()
	rc := http.NewResponseController(w)
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client went away; Close marks the interruption
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
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
