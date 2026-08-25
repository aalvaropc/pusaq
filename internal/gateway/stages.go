package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
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
func Accounting(emit func(core.CostEvent), logger *slog.Logger) core.Stage {
	return func(next core.Handler) core.Handler {
		return func(ctx context.Context, req *core.Request) (resp *core.Response, err error) {
			start := time.Now()
			defer func() {
				event := core.CostEvent{
					RequestID: req.ID,
					Timestamp: req.ArrivedAt,
					Op:        req.Op,
					Outcome:   core.OutcomeSuccess,
					Latency:   time.Since(start),
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
