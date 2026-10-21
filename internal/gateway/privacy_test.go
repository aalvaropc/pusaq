package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

func TestPanicDiagnosticsExcludeSensitiveValue(t *testing.T) {
	var logs bytes.Buffer
	var events []core.CostEvent
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, logger), RequestID())(func(context.Context, *core.Request) (*core.Response, error) { panic("private-diagnostic-sentinel") })
	_, err := h(context.Background(), &core.Request{Op: "test"})
	raw, _ := json.Marshal(events)
	if len(events) != 1 || err == nil || strings.Contains(logs.String()+string(raw)+core.AsError(err).Message, "private-diagnostic-sentinel") {
		t.Fatal("panic diagnostics leaked or accounting changed")
	}
}

func TestFallbackDiagnosticsExcludeWrappedError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	p := &scriptedProvider{name: "mock"}
	_, _, err := viaChain(context.Background(), map[string]provider.Provider{"mock": p}, &core.Request{ID: "request"}, Route{Provider: "mock"}, CompletionsOptions{Logger: logger}, func(provider.Provider) (provider.ChatResponse, error) {
		return provider.ChatResponse{}, &core.Error{Code: core.CodeUpstream, Message: "upstream unavailable", Err: errors.New("private-diagnostic-sentinel")}
	})
	if err == nil || strings.Contains(logs.String(), "private-diagnostic-sentinel") {
		t.Fatal("fallback diagnostics leaked")
	}
}
