// Package gateway is Pusaq's HTTP layer: it adapts HTTP to the
// transport-agnostic core pipeline, renders normalized errors in one JSON
// shape, and owns the operational behavior (liveness, readiness, draining,
// failure injection).
package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

// maxBodyBytes bounds request bodies until per-tenant policy limits exist.
const maxBodyBytes = 1 << 20 // 1 MiB

// RegionHeader reports the region label of the replica that answered
// (PUSAQ_REGION); absent when the replica is unlabeled.
const RegionHeader = "X-Pusaq-Region"

// maxInjectedWindow caps an injected not-ready window so an experiment can
// never leave a region refusing traffic indefinitely.
const maxInjectedWindow = time.Hour

// Server wires the pipeline into an http.Handler and controls readiness.
type Server struct {
	logger   *slog.Logger
	pipeline core.Stage
	ready    atomic.Bool
	mux      *http.ServeMux

	region     string
	adminToken string
	// notReadyUntil is the end of the injected not-ready window in Unix
	// nanoseconds; zero means none. While it is in the future, readiness
	// reports 503 and new requests are refused with not_ready; in-flight
	// requests and streams are untouched.
	notReadyUntil atomic.Int64
	// now is the clock the window is measured against; swapped in tests.
	now func() time.Time
}

// ServerOption configures optional server behavior.
type ServerOption func(*Server)

// WithRegion labels every response with RegionHeader.
func WithRegion(region string) ServerOption {
	return func(s *Server) { s.region = region }
}

// WithAdminToken serves the admin endpoints, guarded by this bearer token.
// Without it they are not registered at all.
func WithAdminToken(token string) ServerOption {
	return func(s *Server) { s.adminToken = token }
}

// NewServer builds the gateway with the given composed pipeline stage and
// the terminal handler for chat completions. The server starts not-ready;
// call SetReady(true) once serving begins.
func NewServer(logger *slog.Logger, pipeline core.Stage, completions core.Handler, opts ...ServerOption) *Server {
	s := &Server{logger: logger, pipeline: pipeline, mux: http.NewServeMux(), now: time.Now}
	for _, opt := range opts {
		opt(s)
	}

	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.Handle("POST /v1/chat/completions", s.pipelineHandler("chat.completions", completions))
	if s.adminToken != "" {
		s.mux.HandleFunc("POST /admin/readiness", s.handleAdminReadiness)
	}

	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.region != "" {
		w.Header().Set(RegionHeader, s.region)
	}
	s.mux.ServeHTTP(w, r)
}

// SetReady flips the readiness flag: true once serving begins, false when
// draining starts. Draining keeps serving new requests for the drain delay
// (the balancer stops sending them); an injected window (see
// handleAdminReadiness) refuses them instead.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// injected reports whether an injected not-ready window is open and how
// long it has left.
func (s *Server) injected() (bool, time.Duration) {
	until := s.notReadyUntil.Load()
	if until == 0 {
		return false, 0
	}
	remaining := time.Duration(until - s.now().UnixNano())
	if remaining <= 0 {
		return false, 0
	}
	return true, remaining
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
	if open, _ := s.injected(); open {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "injected\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ready\n")
}

// handleAdminReadiness is the failure-injection knob chaos experiments use
// (docs/design.md §7, constraint 4): `state=down&for=<duration>` opens a
// not-ready window — readiness answers 503 and new requests are refused
// with not_ready while in-flight ones finish — and `state=up` ends it.
// One log record per action is the observable the measurement reads.
func (s *Server) handleAdminReadiness(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(s.adminToken)) != 1 {
		s.writeError(w, "", core.NewError(core.CodeUnauthorized, "admin token required"))
		return
	}
	q := r.URL.Query()
	now := s.now()
	switch q.Get("state") {
	case "down":
		d, err := time.ParseDuration(q.Get("for"))
		if err != nil || d <= 0 || d > maxInjectedWindow {
			s.writeError(w, "", core.Errorf(core.CodeInvalidRequest, "for must be a positive duration of at most %s", maxInjectedWindow))
			return
		}
		until := now.Add(d)
		s.notReadyUntil.Store(until.UnixNano())
		s.logger.Info("readiness_window",
			slog.String("action", "down"),
			slog.String("region", s.region),
			slog.Duration("for", d),
			slog.String("from", now.UTC().Format(time.RFC3339Nano)),
			slog.String("until", until.UTC().Format(time.RFC3339Nano)),
		)
		s.writeJSON(w, map[string]any{
			"ready":  false,
			"region": s.region,
			"from":   now.UTC().Format(time.RFC3339Nano),
			"until":  until.UTC().Format(time.RFC3339Nano),
		})
	case "up":
		s.notReadyUntil.Store(0)
		s.logger.Info("readiness_window",
			slog.String("action", "up"),
			slog.String("region", s.region),
			slog.String("at", now.UTC().Format(time.RFC3339Nano)),
		)
		s.writeJSON(w, map[string]any{"ready": true, "region": s.region})
	default:
		s.writeError(w, "", core.NewError(core.CodeInvalidRequest, `state must be "down" or "up"`))
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("failed to encode response", slog.Any("error", err))
	}
}

// pipelineHandler adapts HTTP to the core pipeline for one operation: it
// builds the domain envelope, runs the composed stages around the terminal
// handler, stamps the request-ID header on every outcome, and renders
// normalized errors.
func (s *Server) pipelineHandler(op string, terminal core.Handler) http.Handler {
	h := s.pipeline(terminal)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An injected not-ready window refuses requests before admission:
		// no cost event, a retry hint for the window's remainder. Requests
		// already inside the pipeline (including streams) are unaffected.
		if open, remaining := s.injected(); open {
			s.writeError(w, "", &core.Error{Code: core.CodeNotReady, Message: "replica not ready (injected)", RetryAfter: remaining})
			return
		}

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
		if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			req.APIKey = strings.TrimSpace(token)
		}
		resp, err := h(r.Context(), req)
		if err != nil {
			// After model resolution, even errors report the attempted model.
			if req.ServedModel != "" {
				w.Header().Set(ServedModelHeader, req.ServedModel)
			}
			if req.RouteReason != "" {
				w.Header().Set(RouteReasonHeader, req.RouteReason)
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
		Code         core.Code `json:"code"`
		Message      string    `json:"message"`
		RetryAfterMS int64     `json:"retry_after_ms,omitempty"`
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
	if e.RetryAfter > 0 {
		body.Error.RetryAfterMS = e.RetryAfter.Milliseconds()
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	w.WriteHeader(e.Status())
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.Error("failed to encode error response", slog.Any("error", err))
	}
}
