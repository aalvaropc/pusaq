package core

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error code. Codes are part of the public
// API contract: clients may branch on them, so existing values never change.
type Code string

const (
	// CodeInvalidRequest marks a malformed or unprocessable client request.
	CodeInvalidRequest Code = "invalid_request"
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
)

// statusByCode maps each code to the HTTP status the transport layer renders.
var statusByCode = map[Code]int{
	CodeInvalidRequest: http.StatusBadRequest,
	CodeNotFound:       http.StatusNotFound,
	CodeInternal:       http.StatusInternalServerError,
	CodeUpstream:       http.StatusBadGateway,
}

// Error is Pusaq's normalized error: every failure the gateway reports has a
// stable code, an HTTP status, and a human-readable message rendered in one
// JSON shape by the transport layer.
type Error struct {
	Code    Code
	Message string
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
