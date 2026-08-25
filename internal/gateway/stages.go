package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

// RequestIDHeader carries the request ID back to the client. The HTTP
// adapter stamps it on every response — success and error alike — so the
// client, the logs, and the cost event share one correlation key.
const RequestIDHeader = "X-Pusaq-Request-Id"

// newRequestID returns a 16-byte random hex ID. crypto/rand keeps IDs
// unpredictable; collision probability is negligible at gateway scale.
func newRequestID() string {
	var b [16]byte
	// rand.Read never fails on supported platforms (it panics instead).
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RequestID assigns the request its unique ID. It runs immediately inside
// Accounting so the ID exists before any other stage observes the request.
func RequestID() core.Stage {
	return func(next core.Handler) core.Handler {
		return func(ctx context.Context, req *core.Request) (*core.Response, error) {
			if req.ID == "" {
				req.ID = newRequestID()
			}
			return next(ctx, req)
		}
	}
}

// Accounting is the outermost stage of every pipeline. It owns the request's
// cost event and guarantees the constitution's invariant: exactly one event
// per admitted request — on success, on error, and on panic. A panic is
// recovered into a normalized internal error; the process never dies for a
// request. Because stages share the *Request, the deferred emission sees the
// ID assigned by the inner RequestID stage.
//
// A response carrying a Stream outlives the pipeline call: its event is
// finalized when the stream ends instead — see finalizingStream.
func Accounting(emit func(core.CostEvent), logger *slog.Logger) core.Stage {
	return func(next core.Handler) core.Handler {
		return func(ctx context.Context, req *core.Request) (resp *core.Response, err error) {
			start := time.Now()
			defer func() {
				event := core.CostEvent{
					RequestID:      req.ID,
					Timestamp:      req.ArrivedAt,
					Op:             req.Op,
					Outcome:        core.OutcomeSuccess,
					Latency:        time.Since(start),
					RequestedModel: req.RequestedModel,
					ServedModel:    req.ServedModel,
					Usage:          req.Usage,
				}
				if r := recover(); r != nil {
					logger.Error("panic recovered",
						slog.String("request_id", req.ID),
						slog.String("op", req.Op),
						slog.Any("panic", r),
					)
					event.Outcome = core.OutcomePanic
					event.ErrorCode = core.CodeInternal
					resp, err = nil, core.NewError(core.CodeInternal, "internal error")
				} else if err == nil && resp != nil && resp.Stream != nil {
					// The stream outlives this call: hand emission to the
					// finalizing wrapper and skip the deferred one.
					resp.Stream = &finalizingStream{
						inner: resp.Stream,
						req:   req,
						start: start,
						emit:  emit,
					}
					return
				} else if err != nil {
					event.Outcome = core.OutcomeError
					event.ErrorCode = core.AsError(err).Code
				}
				emit(event)
			}()
			return next(ctx, req)
		}
	}
}

// finalizingStream passes the response stream through and emits the request's
// cost event exactly once when the stream ends: EOF (success), read error
// (upstream_error), or Close before EOF (stream_interrupted — the client
// left). Latency covers the full stream; usage is whatever the envelope
// accumulated while the stream was scanned.
type finalizingStream struct {
	inner io.ReadCloser
	req   *core.Request
	start time.Time
	emit  func(core.CostEvent)
	once  sync.Once
	done  bool
}

func (f *finalizingStream) Read(p []byte) (int, error) {
	n, err := f.inner.Read(p)
	switch {
	case err == nil:
	case errors.Is(err, io.EOF):
		f.done = true
		f.finalize(core.OutcomeSuccess, "")
	case errors.Is(err, context.Canceled):
		// The client left: its request context canceled the upstream read.
		f.done = true
		f.finalize(core.OutcomeError, core.CodeStreamInterrupted)
	default:
		f.done = true
		f.finalize(core.OutcomeError, core.CodeUpstream)
	}
	return n, err
}

func (f *finalizingStream) Close() error {
	if !f.done {
		f.finalize(core.OutcomeError, core.CodeStreamInterrupted)
	}
	return f.inner.Close()
}

func (f *finalizingStream) finalize(outcome core.Outcome, code core.Code) {
	f.once.Do(func() {
		f.emit(core.CostEvent{
			RequestID:      f.req.ID,
			Timestamp:      f.req.ArrivedAt,
			Op:             f.req.Op,
			Outcome:        outcome,
			ErrorCode:      code,
			Latency:        time.Since(f.start),
			RequestedModel: f.req.RequestedModel,
			ServedModel:    f.req.ServedModel,
			Usage:          f.req.Usage,
		})
	})
}
