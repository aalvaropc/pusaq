package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

const testAdminToken = "chaos-token-for-tests"

// newOperableServer is newTestServer with a region label and the admin
// endpoint enabled, ready to serve.
func newOperableServer(t *testing.T, opts ...ServerOption) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID())
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": okProvider()}, CompletionsOptions{Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, completions, opts...)
	s.SetReady(true)
	return s, &events
}

func adminReadiness(s *Server, token, query string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/readiness?"+query, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	s.ServeHTTP(rec, req)
	return rec
}

func getReadyz(s *Server) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return rec
}

func postCompletion(s *Server) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	return rec
}

func TestAdminReadinessNotServedWithoutToken(t *testing.T) {
	s, _ := newOperableServer(t) // no WithAdminToken
	if rec := adminReadiness(s, "anything", "state=down&for=1s"); rec.Code != http.StatusNotFound {
		t.Fatalf("admin route without a configured token = %d, want 404", rec.Code)
	}
}

func TestAdminReadinessRejectsWrongToken(t *testing.T) {
	s, _ := newOperableServer(t, WithAdminToken(testAdminToken))
	for _, token := range []string{"", "wrong"} {
		rec := adminReadiness(s, token, "state=down&for=1s")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q = %d, want 401", token, rec.Code)
		}
		var body errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != core.CodeUnauthorized {
			t.Fatalf("token %q body = %s, want normalized %s", token, rec.Body.String(), core.CodeUnauthorized)
		}
	}
	if rec := getReadyz(s); rec.Code != http.StatusOK {
		t.Fatalf("a rejected admin call changed readiness: %d", rec.Code)
	}
}

func TestAdminReadinessRejectsBadParameters(t *testing.T) {
	s, _ := newOperableServer(t, WithAdminToken(testAdminToken))
	for _, q := range []string{"", "state=sideways", "state=down", "state=down&for=0s", "state=down&for=-1s", "state=down&for=nope", "state=down&for=2h"} {
		if rec := adminReadiness(s, testAdminToken, q); rec.Code != http.StatusBadRequest {
			t.Fatalf("query %q = %d, want 400", q, rec.Code)
		}
	}
	if rec := getReadyz(s); rec.Code != http.StatusOK {
		t.Fatalf("a rejected admin call changed readiness: %d", rec.Code)
	}
}

func TestInjectedNotReadyWindowRefusesNewRequestsUntilItEnds(t *testing.T) {
	s, events := newOperableServer(t, WithAdminToken(testAdminToken), WithRegion("us-east1"))
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	rec := adminReadiness(s, testAdminToken, "state=down&for=30s")
	if rec.Code != http.StatusOK {
		t.Fatalf("inject = %d: %s", rec.Code, rec.Body.String())
	}
	var ack struct {
		Ready  bool   `json:"ready"`
		Region string `json:"region"`
		From   string `json:"from"`
		Until  string `json:"until"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("ack is not JSON: %v", err)
	}
	if ack.Ready || ack.Region != "us-east1" || ack.From != now.Format(time.RFC3339Nano) || ack.Until != now.Add(30*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("ack = %+v", ack)
	}

	if rec := getReadyz(s); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "injected") {
		t.Fatalf("readyz during the window = %d %q, want 503 injected", rec.Code, rec.Body.String())
	}

	rec = postCompletion(s)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("completion during the window = %d, want 503", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != core.CodeNotReady {
		t.Fatalf("body = %s, want normalized %s", rec.Body.String(), core.CodeNotReady)
	}
	if body.Error.RetryAfterMS != 30_000 || rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("retry guidance = %d ms / %q, want the remaining window", body.Error.RetryAfterMS, rec.Header().Get("Retry-After"))
	}
	if got := rec.Header().Get(RegionHeader); got != "us-east1" {
		t.Fatalf("%s = %q on the refusal, want us-east1", RegionHeader, got)
	}
	if len(*events) != 0 {
		t.Fatalf("a refused request emitted %d cost events, want 0 (never admitted)", len(*events))
	}

	// The window expires on its own.
	now = now.Add(31 * time.Second)
	if rec := getReadyz(s); rec.Code != http.StatusOK {
		t.Fatalf("readyz after the window = %d, want 200", rec.Code)
	}
	if rec := postCompletion(s); rec.Code != http.StatusOK {
		t.Fatalf("completion after the window = %d, want 200", rec.Code)
	}
	if len(*events) != 1 {
		t.Fatalf("events after recovery = %d, want 1", len(*events))
	}
}

func TestAdminReadinessUpEndsTheWindowEarly(t *testing.T) {
	s, _ := newOperableServer(t, WithAdminToken(testAdminToken))
	if rec := adminReadiness(s, testAdminToken, "state=down&for=10m"); rec.Code != http.StatusOK {
		t.Fatalf("inject = %d", rec.Code)
	}
	if rec := getReadyz(s); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz during the window = %d, want 503", rec.Code)
	}
	rec := adminReadiness(s, testAdminToken, "state=up")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ready":true`) {
		t.Fatalf("clear = %d %s", rec.Code, rec.Body.String())
	}
	if rec := getReadyz(s); rec.Code != http.StatusOK {
		t.Fatalf("readyz after clear = %d, want 200", rec.Code)
	}
	if rec := postCompletion(s); rec.Code != http.StatusOK {
		t.Fatalf("completion after clear = %d, want 200", rec.Code)
	}
}

func TestDrainStillWinsOverAnInjectedWindow(t *testing.T) {
	s, _ := newOperableServer(t, WithAdminToken(testAdminToken))
	s.SetReady(false)
	if rec := getReadyz(s); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "draining") {
		t.Fatalf("readyz while draining = %d %q", rec.Code, rec.Body.String())
	}
	// Draining keeps serving new requests (the balancer stops sending
	// them); only an injected window refuses them.
	if rec := postCompletion(s); rec.Code != http.StatusOK {
		t.Fatalf("completion while draining = %d, want 200", rec.Code)
	}
}

func TestRegionHeaderOnEveryResponse(t *testing.T) {
	s, _ := newOperableServer(t, WithRegion("local-a"))
	if got := postCompletion(s).Header().Get(RegionHeader); got != "local-a" {
		t.Fatalf("%s on success = %q", RegionHeader, got)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest || rec.Header().Get(RegionHeader) != "local-a" {
		t.Fatalf("error response = %d, %s = %q", rec.Code, RegionHeader, rec.Header().Get(RegionHeader))
	}
	if got := getReadyz(s).Header().Get(RegionHeader); got != "local-a" {
		t.Fatalf("%s on readyz = %q", RegionHeader, got)
	}

	plain, _ := newOperableServer(t)
	if got := postCompletion(plain).Header().Get(RegionHeader); got != "" {
		t.Fatalf("%s without a configured region = %q, want absent", RegionHeader, got)
	}
}

// injectingStream is an in-flight streamed response that injects a
// not-ready window from inside its first Read — i.e. while the request is
// being served — and then completes normally.
type injectingStream struct {
	s      *Server
	chunks []string
	closed bool
}

func (i *injectingStream) Read(p []byte) (int, error) {
	if len(i.chunks) == 0 {
		return 0, io.EOF
	}
	if len(i.chunks) == 3 {
		if rec := adminReadiness(i.s, testAdminToken, "state=down&for=1m"); rec.Code != http.StatusOK {
			panic("injection failed: " + rec.Body.String())
		}
	}
	n := copy(p, i.chunks[0])
	i.chunks = i.chunks[1:]
	return n, nil
}

func (i *injectingStream) Close() error { i.closed = true; return nil }

func TestInFlightStreamContinuesAfterInjection(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	var s *Server
	stream := &injectingStream{chunks: []string{"data: a\n\n", "data: b\n\n", "data: [DONE]\n\n"}}
	terminal := func(_ context.Context, _ *core.Request) (*core.Response, error) {
		stream.s = s
		return &core.Response{Status: http.StatusOK, Header: map[string]string{"Content-Type": "text/event-stream"}, Stream: stream}, nil
	}
	s = NewServer(discardLogger(), core.Chain(Accounting(emit, discardLogger()), RequestID()), terminal, WithAdminToken(testAdminToken))
	s.SetReady(true)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))

	if rec.Code != http.StatusOK || !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("in-flight stream = %d %q, want it to complete", rec.Code, rec.Body.String())
	}
	if !stream.closed {
		t.Fatal("stream was not closed")
	}
	if len(events) != 1 || events[0].Outcome != core.OutcomeSuccess {
		t.Fatalf("events = %+v, want one success", events)
	}
	// The window injected mid-stream applies to the next request.
	if rec := postCompletion(s); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("next request = %d, want 503 not_ready", rec.Code)
	}
}

func TestRunIDTravelsFromMetadataToTheCostEvent(t *testing.T) {
	s, events := newOperableServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/fast","metadata":{"run_id":"chaos-2026-09-28"},"messages":[{"role":"user","content":"x"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(*events) != 1 || (*events)[0].RunID != "chaos-2026-09-28" {
		t.Fatalf("events = %+v, want run_id on the event", *events)
	}

	// Even a request that fails after parsing carries its run id.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/nope","metadata":{"run_id":"r2"},"messages":[{"role":"user","content":"x"}]}`)))
	if rec.Code != http.StatusBadRequest || len(*events) != 2 || (*events)[1].RunID != "r2" {
		t.Fatalf("failed request: status %d, events %+v", rec.Code, *events)
	}
}

func TestRunIDIsValidated(t *testing.T) {
	s, events := newOperableServer(t)
	for _, payload := range []string{
		`{"model":"pusaq/fast","metadata":{"run_id":7},"messages":[{"role":"user","content":"x"}]}`,
		`{"model":"pusaq/fast","metadata":{"run_id":"` + strings.Repeat("r", 65) + `"},"messages":[{"role":"user","content":"x"}]}`,
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload)))
		var body errorBody
		if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error.Code != core.CodeInvalidRequest {
			t.Fatalf("payload %s: status %d body %s", payload, rec.Code, rec.Body.String())
		}
	}
	if len(*events) != 2 {
		t.Fatalf("events = %d, want one per invalid request", len(*events))
	}
}

func TestRunIDPresentWhenAnEarlyStageRejects(t *testing.T) {
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	rejecting := core.Stage(func(_ core.Handler) core.Handler {
		return func(_ context.Context, _ *core.Request) (*core.Response, error) {
			return nil, core.NewError(core.CodeRateLimited, "rejected before the handler")
		}
	})
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), Auth(nil), rejecting)
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": okProvider()}, CompletionsOptions{Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, completions)
	s.SetReady(true)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/fast","metadata":{"run_id":"early"},"messages":[{"role":"user","content":"x"}]}`)))
	if rec.Code != http.StatusTooManyRequests || len(events) != 1 || events[0].RunID != "early" {
		t.Fatalf("status %d, events %+v: want the run id on the early rejection's event", rec.Code, events)
	}
}
