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
	"github.com/aalvaropc/pusaq/internal/ratelimit"
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

// DevTenant is the tenant every request is attributed to when no API keys
// are configured (dev mode — explicit, never silent: main logs a warning).
const DevTenant = core.DevTenant

// Auth resolves the request's tenant from its API key. With keys configured,
// a missing or unknown key is rejected with unauthorized (401) before any
// downstream stage runs; with no keys, every request belongs to DevTenant.
func Auth(apiKeys map[string]string) core.Stage {
	return func(next core.Handler) core.Handler {
		next = loadPayload(next)
		return func(ctx context.Context, req *core.Request) (*core.Response, error) {
			if len(apiKeys) == 0 {
				req.Tenant = DevTenant
				return next(ctx, req)
			}
			if req.APIKey == "" {
				return nil, core.NewError(core.CodeUnauthorized, "missing API key")
			}
			tenant, ok := apiKeys[req.APIKey]
			if !ok {
				return nil, core.NewError(core.CodeUnauthorized, "unknown API key")
			}
			req.Tenant = tenant
			return next(ctx, req)
		}
	}
}

// Admitter is the rate-limit decision surface the stage consumes; satisfied
// by *ratelimit.Limiter. An error means the store was unreachable.
type Admitter interface {
	Allow(ctx context.Context, tenant string, cost float64) (ratelimit.Decision, error)
}

// limiterBudget bounds how long the hot path waits for the limiter's store.
// Past it, the request fails open: a degraded limiter may be inexact but
// must never become the latency bottleneck it exists to protect against.
const limiterBudget = 250 * time.Millisecond

// RateLimit rejects requests beyond the tenant's bucket with rate_limited
// (429) and a computed retry. It runs after Auth (it keys by tenant) and
// before any expensive work. Store failure fails OPEN with a logged warning:
// temporarily inexact limits beat an outage caused by the limiter itself —
// unless the tenant's policy is fail-closed, in which case the request is
// rejected with store_unavailable (503).
func RateLimit(limiter Admitter, policies PolicyFor, logger *slog.Logger) core.Stage {
	return func(next core.Handler) core.Handler {
		return func(ctx context.Context, req *core.Request) (*core.Response, error) {
			limCtx, cancel := context.WithTimeout(ctx, limiterBudget)
			d, err := limiter.Allow(limCtx, req.Tenant, 1)
			cancel()
			if err != nil {
				if policies != nil && policies(req.Tenant).FailClosed {
					logger.Warn("rate limiter store unavailable, failing closed",
						slog.String("request_id", req.ID),
						slog.String("tenant", req.Tenant),
						slog.String("error_code", string(core.AsError(err).Code)),
					)
					return nil, &core.Error{Code: core.CodeStoreUnavailable, Message: "rate limiter store unavailable", Err: err}
				}
				logger.Warn("rate limiter store unavailable, failing open",
					slog.String("request_id", req.ID),
					slog.String("tenant", req.Tenant),
					slog.String("error_code", string(core.AsError(err).Code)),
				)
				return next(ctx, req)
			}
			if !d.Allowed {
				return nil, &core.Error{
					Code:       core.CodeRateLimited,
					Message:    "request rate limit exceeded",
					RetryAfter: d.RetryAfter,
				}
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
				finalized := false
				finalize := func() {
					if !finalized {
						finalized = true
						req.RunFinalizers()
					}
				}
				event := core.CostEvent{
					RequestID:      req.ID,
					Timestamp:      req.ArrivedAt,
					Op:             req.Op,
					Tenant:         req.Tenant,
					Outcome:        core.OutcomeSuccess,
					Latency:        time.Since(start),
					RequestedModel: req.RequestedModel,
					ServedModel:    req.ServedModel,
					FeatureTag:     req.FeatureTag,
					RouteReason:    req.RouteReason,
					RunID:          req.RunID,
					PolicyFlags:    req.PolicyFlags,
					CacheSkip:      req.CacheSkip,
					Usage:          req.Usage,
				}
				if r := recover(); r != nil {
					logger.Error("panic recovered",
						slog.String("request_id", req.ID),
						slog.String("op", req.Op),
					)
					event.Outcome = core.OutcomePanic
					event.ErrorCode = core.CodeInternal
					resp, err = nil, core.NewError(core.CodeInternal, "internal error")
				} else if err == nil && resp != nil && resp.Stream != nil {
					// The stream outlives this call: hand emission (and
					// finalizers) to the finalizing wrapper.
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
				finalize()
				event.Usage = req.Usage
				event.TokensCharged = req.TokensCharged
				event.ServedProvider = req.ServedProvider
				event.Attempts = req.Attempts
				event.Cost = req.Cost
				event.SpendUSDMonth = req.SpendUSDMonth
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
		f.req.RunFinalizers()
		f.emit(core.CostEvent{
			RequestID:      f.req.ID,
			Timestamp:      f.req.ArrivedAt,
			Op:             f.req.Op,
			Tenant:         f.req.Tenant,
			Outcome:        outcome,
			ErrorCode:      code,
			Latency:        time.Since(f.start),
			RequestedModel: f.req.RequestedModel,
			ServedModel:    f.req.ServedModel,
			FeatureTag:     f.req.FeatureTag,
			RouteReason:    f.req.RouteReason,
			RunID:          f.req.RunID,
			PolicyFlags:    f.req.PolicyFlags,
			CacheSkip:      f.req.CacheSkip,
			Usage:          f.req.Usage,
			TokensCharged:  f.req.TokensCharged,
			ServedProvider: f.req.ServedProvider,
			Attempts:       f.req.Attempts,
			Cost:           f.req.Cost,
			SpendUSDMonth:  f.req.SpendUSDMonth,
		})
	})
}

func loadPayload(next core.Handler) core.Handler {
	return func(ctx context.Context, req *core.Request) (*core.Response, error) {
		if req.ReadPayload != nil {
			read := req.ReadPayload
			req.ReadPayload = nil
			body, err := read()
			if err != nil {
				return nil, err
			}
			runID, err := runIDFromPayload(body)
			if err != nil {
				return nil, err
			}
			req.Payload, req.RunID = body, runID
		}
		return next(ctx, req)
	}
}
