package core

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestErrorStatusMapping(t *testing.T) {
	tests := []struct {
		code Code
		want int
	}{
		{CodeInvalidRequest, http.StatusBadRequest},
		{CodeNotFound, http.StatusNotFound},
		{CodeInternal, http.StatusInternalServerError},
		{CodePolicyDenied, http.StatusForbidden},
		{CodeBudgetExceeded, http.StatusPaymentRequired},
		{CodeStoreUnavailable, http.StatusServiceUnavailable},
		{CodeClientCanceled, 499},
		{Code("unregistered"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			if got := NewError(tt.code, "m").Status(); got != tt.want {
				t.Fatalf("Status() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAsErrorNormalization(t *testing.T) {
	t.Run("passes through a normalized error, even wrapped", func(t *testing.T) {
		orig := NewError(CodeNotFound, "missing")
		wrapped := fmt.Errorf("stage failed: %w", orig)
		if got := AsError(wrapped); got != orig {
			t.Fatalf("AsError(wrapped) = %v, want the original *Error", got)
		}
	})

	t.Run("maps unknown errors to internal without leaking details", func(t *testing.T) {
		got := AsError(errors.New("dial tcp 10.0.0.1: connection refused"))
		if got.Code != CodeInternal {
			t.Fatalf("Code = %s, want %s", got.Code, CodeInternal)
		}
		if got.Message != "internal error" {
			t.Fatalf("Message = %q leaks internals", got.Message)
		}
	})
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	e := &Error{Code: CodeInternal, Message: "boom", Err: cause}
	if !errors.Is(e, cause) {
		t.Fatal("errors.Is should reach the wrapped cause")
	}
}

func TestErrorTypeMapping(t *testing.T) {
	tests := []struct {
		code       Code
		wantStatus int
		wantType   string
	}{
		{CodeInvalidRequest, http.StatusBadRequest, "invalid_request_error"},
		{CodeUnauthorized, http.StatusUnauthorized, "authentication_error"},
		{CodeBudgetExceeded, http.StatusPaymentRequired, "insufficient_quota"},
		{CodePolicyDenied, http.StatusForbidden, "permission_error"},
		{CodeNotFound, http.StatusNotFound, "not_found_error"},
		{CodeMethodNotAllowed, http.StatusMethodNotAllowed, "invalid_request_error"},
		{CodeRateLimited, http.StatusTooManyRequests, "rate_limit_error"},
		{CodeClientCanceled, 499, "invalid_request_error"},
		{CodeInternal, http.StatusInternalServerError, "server_error"},
		{CodeUpstream, http.StatusBadGateway, "server_error"},
		{CodeStoreUnavailable, http.StatusServiceUnavailable, "server_error"},
		{CodeNotReady, http.StatusServiceUnavailable, "server_error"},
		{Code("unregistered"), http.StatusInternalServerError, "server_error"},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			e := NewError(tt.code, "m")
			if got := e.Status(); got != tt.wantStatus {
				t.Fatalf("Status() = %d, want %d", got, tt.wantStatus)
			}
			if got := e.Type(); got != tt.wantType {
				t.Fatalf("Type() = %q, want %q", got, tt.wantType)
			}
		})
	}
}

func TestInvalidFieldNamesTheParam(t *testing.T) {
	e := InvalidField("metadata.run_id", "metadata.run_id must be at most %d characters", 64)
	if e.Code != CodeInvalidRequest || e.Param != "metadata.run_id" || e.Message != "metadata.run_id must be at most 64 characters" {
		t.Fatalf("InvalidField() = %+v", e)
	}
}
