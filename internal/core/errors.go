package core

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Code is a stable, machine-readable error code. Codes are part of the public
// API contract: clients may branch on them, so existing values never change.
type Code string

const (
	// CodeInvalidRequest marks a malformed or unprocessable client request.
	CodeInvalidRequest Code = "invalid_request"
	// CodeUnauthorized marks a request with a missing or unknown API key.
	CodeUnauthorized Code = "unauthorized"
	// CodeRateLimited marks a request rejected by a rate limit; the error
	// carries RetryAfter so clients know when to come back.
	CodeRateLimited Code = "rate_limited"
	// CodeNotFound marks a missing route or resource.
	CodeNotFound Code = "not_found"
	// CodeInternal marks an unexpected gateway failure. It is the fallback
	// for unknown errors and never leaks internal details to the client.
	CodeInternal Code = "internal"
	// CodeUpstream marks a provider failure or timeout. The provider's raw
	// response is never forwarded to the client.
	CodeUpstream Code = "upstream_error"
	// CodeStreamInterrupted marks a streamed response the client abandoned
	// before completion. Accounting-only: nothing is written to a gone
	// client.
	CodeStreamInterrupted Code = "stream_interrupted"
	// CodePolicyDenied marks a request the tenant's policy forbids (model
	// not allowed, prompt too large).
	CodePolicyDenied Code = "policy_denied"
	// CodeBudgetExceeded marks a request refused because the tenant's
	// monthly budget is exhausted; RetryAfter points at the next month.
	CodeBudgetExceeded Code = "budget_exceeded"
	// CodeStoreUnavailable marks a request refused because a limit's store
	// was unreachable and the tenant's policy is fail-closed.
	CodeStoreUnavailable Code = "store_unavailable"
	// CodeNotReady marks a request refused because this replica is inside
	// an injected not-ready window (a failure-injection experiment); the
	// error carries RetryAfter with the window's remaining time.
	CodeNotReady Code = "not_ready"
)

// statusByCode maps each code to the HTTP status the transport layer renders.
var statusByCode = map[Code]int{
	CodeInvalidRequest: http.StatusBadRequest,
	CodeUnauthorized:   http.StatusUnauthorized,
	CodeRateLimited:    http.StatusTooManyRequests,
	CodeNotFound:       http.StatusNotFound,
	CodeInternal:       http.StatusInternalServerError,
	CodeUpstream:       http.StatusBadGateway,
	CodePolicyDenied:   http.StatusForbidden,
	// 402 rather than 429: SDKs retry 429 with backoff, which is wrong for
	// a budget that only resets next month.
	CodeBudgetExceeded:   http.StatusPaymentRequired,
	CodeStoreUnavailable: http.StatusServiceUnavailable,
	CodeNotReady:         http.StatusServiceUnavailable,
}

// Error is Pusaq's normalized error: every failure the gateway reports has a
// stable code, an HTTP status, and a human-readable message rendered in one
// JSON shape by the transport layer.
type Error struct {
	Code    Code
	Message string
	// RetryAfter, when positive, tells the client when to retry; rendered
	// as retry_after_ms in the body and a Retry-After header.
	RetryAfter time.Duration
	// Err is the wrapped cause, for logs only — never rendered to clients.
	Err error
}

// NewError builds a normalized Error with the given code and message.
func NewError(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Errorf builds a normalized Error with a formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the cause to errors.Is/errors.As.
func (e *Error) Unwrap() error { return e.Err }

// Status returns the HTTP status for the error's code.
func (e *Error) Status() int {
	if s, ok := statusByCode[e.Code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// AsError normalizes any error into an *Error. A non-normalized error maps to
// CodeInternal with a generic message so internals never leak to clients.
func AsError(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	return &Error{Code: CodeInternal, Message: "internal error", Err: err}
}
