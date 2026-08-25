package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

func newTestServer(t *testing.T) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID())
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": okProvider()}, CompletionsOptions{Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions), &events
}

func TestHealthzAlwaysOK(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", rec.Code)
	}
}

func TestReadyzFollowsReadinessFlag(t *testing.T) {
	s, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz before SetReady = %d, want 503", rec.Code)
	}

	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz after SetReady(true) = %d, want 200", rec.Code)
	}

	s.SetReady(false)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz while draining = %d, want 503", rec.Code)
	}
}

func TestCompletionsTraversesPipelineAndEmitsCostEvent(t *testing.T) {
	s, events := newTestServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))

	if rec.Code != http.StatusOK {
		t.Fatalf("completions = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get(ServedModelHeader); got != "mock-model" {
		t.Fatalf("%s = %q, want mock-model", ServedModelHeader, got)
	}
	id := rec.Header().Get(RequestIDHeader)
	if id == "" {
		t.Fatal("response missing request-ID header")
	}
	if len(*events) != 1 {
		t.Fatalf("emitted %d cost events, want 1", len(*events))
	}
	e := (*events)[0]
	if e.RequestID != id {
		t.Fatalf("cost event ID %q != header ID %q", e.RequestID, id)
	}
	if e.RequestedModel != "pusaq/fast" || e.ServedModel != "mock-model" {
		t.Fatalf("cost event models = %q/%q", e.RequestedModel, e.ServedModel)
	}
	if e.Usage.PromptTokens != 5 || e.Usage.CompletionTokens != 2 {
		t.Fatalf("cost event usage = %+v", e.Usage)
	}
	if e.Op != "chat.completions" {
		t.Fatalf("cost event op = %q", e.Op)
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown route = %d, want 404", rec.Code)
	}
}

func TestOversizedBodyRejectedNormalized(t *testing.T) {
	s, events := newTestServer(t)
	big := strings.Repeat("a", maxBodyBytes+1)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(big)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body = %d, want 400", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not the normalized shape: %v", err)
	}
	if body.Error.Code != core.CodeInvalidRequest {
		t.Fatalf("error code = %q, want %q", body.Error.Code, core.CodeInvalidRequest)
	}
	// Rejected before pipeline admission: no cost event yet (admission
	// accounting for pre-pipeline rejections arrives with the limiter slice).
	if len(*events) != 0 {
		t.Fatalf("emitted %d cost events for a pre-admission rejection, want 0", len(*events))
	}
}

func TestPipelineErrorRenderedNormalizedWithRequestID(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	failing := core.Stage(func(_ core.Handler) core.Handler {
		return func(_ context.Context, _ *core.Request) (*core.Response, error) {
			return nil, core.NewError(core.CodeInvalidRequest, "rejected by stage")
		}
	})
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), failing)
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": okProvider()}, CompletionsOptions{Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, completions)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not the normalized shape: %v", err)
	}
	if body.Error.Code != core.CodeInvalidRequest || body.Error.Message != "rejected by stage" {
		t.Fatalf("error body = %+v", body)
	}
	if rec.Header().Get(RequestIDHeader) == "" {
		t.Fatal("error response missing request-ID header")
	}
	if len(events) != 1 || events[0].Outcome != core.OutcomeError {
		t.Fatalf("events = %+v, want one error event", events)
	}
}
