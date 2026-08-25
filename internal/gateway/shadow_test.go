package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/semcache"
)

// recordingShadow captures offered samples.
type recordingShadow struct {
	mu      sync.Mutex
	samples []semcache.Sample
}

func (r *recordingShadow) Offer(s semcache.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, s)
}

func (r *recordingShadow) all() []semcache.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]semcache.Sample(nil), r.samples...)
}

func shadowServer(t *testing.T, p provider.Provider, shadow ShadowCache) *Server {
	t.Helper()
	pipeline := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil))
	completions := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Shadow: shadow, Logger: discardLogger()})
	return NewServer(discardLogger(), pipeline, completions)
}

func TestShadowOfferedForBufferedSuccess(t *testing.T) {
	shadow := &recordingShadow{}
	s := shadowServer(t, okProvider(), shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/fast","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello there"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	samples := shadow.all()
	if len(samples) != 1 {
		t.Fatalf("offered %d samples, want 1", len(samples))
	}
	sm := samples[0]
	if sm.Prompt != "hello there" {
		t.Fatalf("prompt = %q", sm.Prompt)
	}
	if sm.Namespace != semcache.Namespace(DevTenant, "mock-model", "be brief\n") {
		t.Fatalf("namespace = %q, want tenant+model+system derivation", sm.Namespace)
	}
	if sm.Fingerprint == "" || sm.RequestID == "" {
		t.Fatalf("sample = %+v", sm)
	}
}

func TestShadowOfferedAtStreamEndWithDoneGate(t *testing.T) {
	shadow := &recordingShadow{}
	p := okProvider()
	p.sse = fakeSSE
	s := shadowServer(t, p, shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	samples := shadow.all()
	if len(samples) != 1 {
		t.Fatalf("offered %d samples, want 1 at stream end", len(samples))
	}
	// Fingerprint covers the scanned deltas ("Hello world").
	if samples[0].Fingerprint != semcache.Fingerprint("Hello world") {
		t.Fatalf("fingerprint = %q, want hash of streamed content", samples[0].Fingerprint)
	}
}

func TestShadowNotOfferedForAbortedStream(t *testing.T) {
	shadow := &recordingShadow{}
	p := okProvider()
	p.sse = fakeSSE
	pipeline := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil))
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p},
		CompletionsOptions{Shadow: shadow, Logger: discardLogger()})

	resp, err := pipeline(h)(t.Context(), &core.Request{Op: "chat.completions", Payload: streamingPayload()})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Stream.Close() // abandoned before [DONE]

	if got := len(shadow.all()); got != 0 {
		t.Fatalf("offered %d samples for an aborted stream, want 0", got)
	}
}

func TestShadowNotOfferedForMultiTurn(t *testing.T) {
	shadow := &recordingShadow{}
	s := shadowServer(t, okProvider(), shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"pusaq/fast","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := len(shadow.all()); got != 0 {
		t.Fatalf("offered %d samples for multi-turn, want 0", got)
	}
}

func TestShadowNotOfferedOnFailure(t *testing.T) {
	shadow := &recordingShadow{}
	p := okProvider()
	p.err = core.NewError(core.CodeUpstream, "down")
	s := shadowServer(t, p, shadow)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := len(shadow.all()); got != 0 {
		t.Fatalf("offered %d samples for a failed request, want 0", got)
	}
}

func TestNilShadowIsSilent(t *testing.T) {
	s := shadowServer(t, okProvider(), nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(validPayload()))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCacheableShapes(t *testing.T) {
	tests := []struct {
		name string
		msgs []provider.Message
		ok   bool
	}{
		{"single user", []provider.Message{{Role: "user", Content: "q"}}, true},
		{"system then user", []provider.Message{{Role: "system", Content: "s"}, {Role: "user", Content: "q"}}, true},
		{"two users", []provider.Message{{Role: "user", Content: "a"}, {Role: "user", Content: "b"}}, false},
		{"assistant turn", []provider.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}, false},
		{"system after user", []provider.Message{{Role: "user", Content: "a"}, {Role: "system", Content: "s"}}, false},
		{"no user", []provider.Message{{Role: "system", Content: "s"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := cacheable(tt.msgs)
			if ok != tt.ok {
				t.Fatalf("cacheable = %v, want %v", ok, tt.ok)
			}
		})
	}
}
