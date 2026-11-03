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

func TestRouterErrorsUseTheNormalizedShape(t *testing.T) {
	tests := []struct {
		method, path string
		admin        bool
		wantStatus   int
		wantCode     core.Code
		wantType     string
		wantAllow    string
	}{
		{http.MethodGet, "/nope", false, http.StatusNotFound, core.CodeNotFound, "not_found_error", ""},
		{http.MethodPost, "/v1/models", false, http.StatusNotFound, core.CodeNotFound, "not_found_error", ""},
		{http.MethodPost, "/v1/chat/completions/", false, http.StatusNotFound, core.CodeNotFound, "not_found_error", ""},
		{http.MethodPost, "/admin/readiness", false, http.StatusNotFound, core.CodeNotFound, "not_found_error", ""},
		{http.MethodGet, "/v1/chat/completions", false, http.StatusMethodNotAllowed, core.CodeMethodNotAllowed, "invalid_request_error", "POST"},
		{http.MethodPost, "/healthz", false, http.StatusMethodNotAllowed, core.CodeMethodNotAllowed, "invalid_request_error", "GET, HEAD"},
		{http.MethodDelete, "/readyz", false, http.StatusMethodNotAllowed, core.CodeMethodNotAllowed, "invalid_request_error", "GET, HEAD"},
		{http.MethodGet, "/admin/readiness", true, http.StatusMethodNotAllowed, core.CodeMethodNotAllowed, "invalid_request_error", "POST"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			opts := []ServerOption{WithRegion("local-a")}
			if tt.admin {
				opts = append(opts, WithAdminToken(testAdminToken))
			}
			s, events := newOperableServer(t, opts...)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			if got := rec.Header().Get("Allow"); got != tt.wantAllow {
				t.Fatalf("Allow = %q, want %q", got, tt.wantAllow)
			}
			if rec.Header().Get(RequestIDHeader) == "" || rec.Header().Get(RegionHeader) != "local-a" {
				t.Fatalf("headers = %v, want a request ID and the region", rec.Header())
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %q is not the normalized shape: %v", rec.Body.String(), err)
			}
			if body.Error.Code != tt.wantCode || body.Error.Type != tt.wantType || body.Error.Param != nil || body.Error.Message == "" {
				t.Fatalf("body = %s", rec.Body.String())
			}
			if len(*events) != 0 {
				t.Fatalf("a router error emitted %d cost events, want 0", len(*events))
			}
		})
	}
}

func TestErrorBodyCarriesTypeAndParam(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		payload   string
		wantCode  core.Code
		wantType  string
		wantParam string
	}{
		{"validation names its field", "sk-support", `{"messages":[{"role":"user","content":"x"}]}`, core.CodeInvalidRequest, "invalid_request_error", `"model"`},
		{"run id names its field", "sk-support", `{"model":"pusaq/fast","metadata":{"run_id":7},"messages":[{"role":"user","content":"x"}]}`, core.CodeInvalidRequest, "invalid_request_error", `"metadata.run_id"`},
		{"body error has no field", "sk-support", `{`, core.CodeInvalidRequest, "invalid_request_error", "null"},
		{"authentication error", "sk-nope", string(validPayload()), core.CodeUnauthorized, "authentication_error", "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newAuthedServer(t)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.payload))
			req.Header.Set("Authorization", "Bearer "+tt.key)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)

			var raw struct {
				Error map[string]json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("body %q: %v", rec.Body.String(), err)
			}
			if got := string(raw.Error["code"]); got != `"`+string(tt.wantCode)+`"` {
				t.Fatalf("code = %s, want %s", got, tt.wantCode)
			}
			if got := string(raw.Error["type"]); got != `"`+tt.wantType+`"` {
				t.Fatalf("type = %s, want %s", got, tt.wantType)
			}
			if got, ok := raw.Error["param"]; !ok || string(got) != tt.wantParam {
				t.Fatalf("param = %s (present %v), want %s", got, ok, tt.wantParam)
			}
		})
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
	if len(*events) != 1 || (*events)[0].ErrorCode != core.CodeInvalidRequest {
		t.Fatalf("events = %+v, want one invalid request event", *events)
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
