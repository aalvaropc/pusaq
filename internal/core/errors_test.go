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
