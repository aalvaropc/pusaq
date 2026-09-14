package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

func routedRoutes() map[string]Route {
	return map[string]Route{
		"pusaq/fast": {Provider: "mock", Model: "model-a"},
		"pusaq/best": {Provider: "mock", Model: "model-b"},
	}
}

func testRules() RoutingRules {
	return RoutingRules{ByTag: map[string]string{"reason": "pusaq/best", "classify": "pusaq/fast"}, Default: "pusaq/fast"}
}

// routedServer wires the handler with routing rules and captures events.
func routedServer(t *testing.T, p provider.Provider) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID())
	completions := CompletionsHandler(routedRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Rules: testRules(), Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions), &events
}

func post(s *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	return rec
}

func TestResolveReasons(t *testing.T) {
	r := testRules()
	tests := []struct {
		requested, tag, wantAlias, wantReason string
	}{
		{"pusaq/fast", "", "pusaq/fast", "direct"},
		{"pusaq/best", "reason", "pusaq/best", "direct"},
		{AutoAlias, "reason", "pusaq/best", "rule:reason"},
		{AutoAlias, "", "pusaq/fast", "default:untagged"},
		{AutoAlias, "poetry", "pusaq/fast", "default:unknown_tag"},
	}
	for _, tt := range tests {
		alias, reason := r.resolve(tt.requested, tt.tag)
		if alias != tt.wantAlias || reason != tt.wantReason {
			t.Fatalf("resolve(%q, %q) = %q/%q, want %q/%q", tt.requested, tt.tag, alias, reason, tt.wantAlias, tt.wantReason)
		}
	}
}

func TestAutoRoutesByFeatureTag(t *testing.T) {
	p := okProvider()
	s, events := routedServer(t, p)
	rec := post(s, `{"model":"pusaq/auto","metadata":{"feature_tag":"reason"},"messages":[{"role":"user","content":"x"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if p.got.Model != "model-b" {
		t.Fatalf("provider got model %q, want model-b", p.got.Model)
	}
	if got := rec.Header().Get(ServedModelHeader); got != "model-b" {
		t.Fatalf("%s = %q", ServedModelHeader, got)
	}
	if got := rec.Header().Get(RouteReasonHeader); got != "rule:reason" {
		t.Fatalf("%s = %q", RouteReasonHeader, got)
	}
	e := (*events)[0]
	if e.RequestedModel != "pusaq/auto" || e.ServedModel != "model-b" || e.FeatureTag != "reason" || e.RouteReason != "rule:reason" {
		t.Fatalf("event = %+v", e)
	}
}

func TestAutoUntaggedUsesDefault(t *testing.T) {
	p := okProvider()
	s, events := routedServer(t, p)
	rec := post(s, `{"model":"pusaq/auto","messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusOK || p.got.Model != "model-a" {
		t.Fatalf("status=%d model=%q", rec.Code, p.got.Model)
	}
	if got := rec.Header().Get(RouteReasonHeader); got != "default:untagged" {
		t.Fatalf("%s = %q", RouteReasonHeader, got)
	}
	if e := (*events)[0]; e.FeatureTag != "" || e.RouteReason != "default:untagged" {
		t.Fatalf("event = %+v", e)
	}
}

func TestAutoUnknownTagUsesDefault(t *testing.T) {
	p := okProvider()
	s, events := routedServer(t, p)
	rec := post(s, `{"model":"pusaq/auto","metadata":{"feature_tag":"poetry"},"messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusOK || p.got.Model != "model-a" {
		t.Fatalf("status=%d model=%q", rec.Code, p.got.Model)
	}
	if got := rec.Header().Get(RouteReasonHeader); got != "default:unknown_tag" {
		t.Fatalf("%s = %q", RouteReasonHeader, got)
	}
	if e := (*events)[0]; e.FeatureTag != "poetry" {
		t.Fatalf("event feature_tag = %q, want the tag recorded even when unmatched", e.FeatureTag)
	}
}

func TestDirectAliasRecordsTagAndReasonDirect(t *testing.T) {
	p := okProvider()
	s, events := routedServer(t, p)
	rec := post(s, `{"model":"pusaq/best","metadata":{"feature_tag":"classify"},"messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusOK || p.got.Model != "model-b" {
		t.Fatalf("status=%d model=%q, want the alias honoured over the tag", rec.Code, p.got.Model)
	}
	if got := rec.Header().Get(RouteReasonHeader); got != "direct" {
		t.Fatalf("%s = %q", RouteReasonHeader, got)
	}
	if e := (*events)[0]; e.FeatureTag != "classify" || e.RouteReason != "direct" {
		t.Fatalf("event = %+v", e)
	}
}

func TestRouteReasonOnUpstreamError(t *testing.T) {
	p := &fakeProvider{name: "mock", err: core.NewError(core.CodeUpstream, "upstream provider returned status 500")}
	s, _ := routedServer(t, p)
	rec := post(s, `{"model":"pusaq/auto","metadata":{"feature_tag":"reason"},"messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if got := rec.Header().Get(RouteReasonHeader); got != "rule:reason" {
		t.Fatalf("%s = %q on error, want the resolution reported", RouteReasonHeader, got)
	}
}

func TestStreamedAutoCarriesRouteReason(t *testing.T) {
	p := okProvider()
	p.sse = fakeSSE
	s, events := routedServer(t, p)
	rec := post(s, `{"model":"pusaq/auto","metadata":{"feature_tag":"reason"},"messages":[{"role":"user","content":"x"}],"stream":true}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Header().Get(RouteReasonHeader); got != "rule:reason" {
		t.Fatalf("%s = %q", RouteReasonHeader, got)
	}
	if e := (*events)[0]; e.RouteReason != "rule:reason" || e.ServedModel != "model-b" {
		t.Fatalf("stream event = %+v", e)
	}
}

func TestNonStringFeatureTagRejected(t *testing.T) {
	p := okProvider()
	s, events := routedServer(t, p)
	rec := post(s, `{"model":"pusaq/auto","metadata":{"feature_tag":7},"messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if p.got.Model != "" {
		t.Fatal("provider was called for a rejected request")
	}
	if e := (*events)[0]; e.ErrorCode != core.CodeInvalidRequest {
		t.Fatalf("event = %+v", e)
	}
}

func TestAutoWithoutRulesIsUnknownModel(t *testing.T) {
	h := CompletionsHandler(routedRoutes(), map[string]provider.Provider{"mock": okProvider()}, CompletionsOptions{Logger: discardLogger()})
	_, err := h(t.Context(), &core.Request{Payload: []byte(`{"model":"pusaq/auto","messages":[{"role":"user","content":"x"}]}`)})
	if core.AsError(err).Code != core.CodeInvalidRequest {
		t.Fatalf("err = %v, want invalid_request", err)
	}
}
