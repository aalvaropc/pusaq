package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

func authKeys() map[string]string {
	return map[string]string{"sk-support": "team-support", "sk-docs": "team-docs"}
}

func TestAuthStage(t *testing.T) {
	tests := []struct {
		name       string
		keys       map[string]string
		apiKey     string
		wantTenant string
		wantCode   core.Code // empty means success expected
	}{
		{"valid key resolves tenant", authKeys(), "sk-support", "team-support", ""},
		{"second key resolves its tenant", authKeys(), "sk-docs", "team-docs", ""},
		{"missing key rejected", authKeys(), "", "", core.CodeUnauthorized},
		{"unknown key rejected", authKeys(), "sk-nope", "", core.CodeUnauthorized},
		{"dev mode without keys", nil, "", DevTenant, ""},
		{"dev mode ignores stray keys", nil, "sk-anything", DevTenant, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawTenant string
			terminal := func(_ context.Context, req *core.Request) (*core.Response, error) {
				sawTenant = req.Tenant
				return &core.Response{Status: http.StatusOK}, nil
			}
			req := &core.Request{APIKey: tt.apiKey}
			_, err := Auth(tt.keys)(terminal)(context.Background(), req)

			if tt.wantCode != "" {
				var norm *core.Error
				if !errors.As(err, &norm) || norm.Code != tt.wantCode {
					t.Fatalf("err = %v, want %s", err, tt.wantCode)
				}
				if sawTenant != "" {
					t.Fatal("terminal handler ran for a rejected request")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sawTenant != tt.wantTenant {
				t.Fatalf("tenant = %q, want %q", sawTenant, tt.wantTenant)
			}
		})
	}
}

func newAuthedServer(t *testing.T) (*Server, *[]core.CostEvent) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	pipeline := core.Chain(Accounting(emit, discardLogger()), RequestID(), Auth(authKeys()))
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": okProvider()})
	return NewServer(discardLogger(), pipeline, completions), &events
}

func TestAuthEndToEnd(t *testing.T) {
	s, events := newAuthedServer(t)

	// Without a key: normalized 401, cost event with the error outcome.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without key = %d, want 401", rec.Code)
	}
	if len(*events) != 1 || (*events)[0].ErrorCode != core.CodeUnauthorized {
		t.Fatalf("events = %+v, want one unauthorized rejection", *events)
	}

	// With a key: 200 and the tenant on the cost event.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload())))
	req.Header.Set("Authorization", "Bearer sk-support")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with key = %d, want 200", rec.Code)
	}
	if len(*events) != 2 {
		t.Fatalf("emitted %d events, want 2", len(*events))
	}
	e := (*events)[1]
	if e.Tenant != "team-support" || e.Outcome != core.OutcomeSuccess {
		t.Fatalf("event = %+v, want team-support success", e)
	}
}

func TestOperationalEndpointsUnauthenticated(t *testing.T) {
	s, _ := newAuthedServer(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("%s demanded credentials", path)
		}
	}
}
