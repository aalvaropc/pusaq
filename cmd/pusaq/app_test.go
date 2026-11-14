package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/config"
	"github.com/aalvaropc/pusaq/internal/gateway"

	"github.com/alicebob/miniredis/v2"
)

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) find(t *testing.T, msg string) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(l.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		if rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

func (l *logBuffer) waitFor(t *testing.T, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(l.find(t, msg)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no %q log record", msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func testApp(t *testing.T, env map[string]string) (*app, *logBuffer) {
	t.Helper()
	cfg, err := config.Load(lookupFrom(env))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	logs := &logBuffer{}
	a, err := newApp(cfg, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	t.Cleanup(a.close)
	return a, logs
}

type fakeUpstream struct {
	status  int
	hits    atomic.Int32
	arrived chan struct{}
	release chan struct{}
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits.Add(1)
	if f.arrived != nil {
		f.arrived <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	_, _ = fmt.Fprintf(w, `{"id":"cmpl-t","object":"chat.completion","created":1700000000,"model":%q,`+
		`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
		`"usage":{"prompt_tokens":3,"completion_tokens":2}}`, req.Model)
}

func upstreamURL(t *testing.T, f *fakeUpstream) string {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func complete(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func prompt(model, content string) string {
	return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}]}`, model, content)
}

func TestDevelopmentModeWarning(t *testing.T) {
	const warning = "authentication disabled: explicit development mode, all requests attributed to the dev tenant"
	tests := []struct {
		name string
		env  map[string]string
		want int
	}{
		{"development mode", map[string]string{"PUSAQ_DEV_MODE": "true"}, 1},
		{"API keys", map[string]string{"PUSAQ_API_KEYS": "sk-a:team-a"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, logs := testApp(t, tt.env)
			got := logs.find(t, warning)
			if len(got) != tt.want {
				t.Fatalf("warnings = %v, want %d", got, tt.want)
			}
			if tt.want == 1 && got[0]["level"] != "WARN" {
				t.Fatalf("level = %v, want WARN", got[0]["level"])
			}
		})
	}
}

func TestServerSettingsFromConfig(t *testing.T) {
	a, logs := testApp(t, map[string]string{
		"PUSAQ_DEV_MODE":           "true",
		"PUSAQ_BODY_READ_TIMEOUT":  "3s",
		"PUSAQ_WRITE_IDLE_TIMEOUT": "4s",
		"PUSAQ_REGION":             "us-east1",
		"PUSAQ_ADMIN_TOKEN":        "admin-token-0123456789",
	})
	if a.srv.BodyReadTimeout != 3*time.Second || a.srv.WriteIdleTimeout != 4*time.Second {
		t.Fatalf("server timeouts = body %v write %v, want 3s and 4s", a.srv.BodyReadTimeout, a.srv.WriteIdleTimeout)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/readiness?state=up", nil)
	req.Header.Set("Authorization", "Bearer admin-token-0123456789")
	rec := httptest.NewRecorder()
	a.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get(gateway.RegionHeader) != "us-east1" {
		t.Fatalf("admin readiness = %d, region %q", rec.Code, rec.Header().Get(gateway.RegionHeader))
	}
	if len(logs.find(t, "admin endpoints enabled: readiness can be failed on purpose with the admin token")) != 1 {
		t.Fatal("admin endpoints enabled without a warning")
	}
}

func TestWiringEnforcesRequestAndTokenLimits(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		redis    bool
		wantLog  string
		wantRPM  bool
		leased   bool
		wantStop string
	}{
		{"strict", "strict", true, "rate limiting enabled", true, false, ""},
		{"lease", "lease", true, "rate limiting enabled", true, true, "lease-mode limiter stopped"},
		{"without a store", "strict", false, "rate limiting disabled: no PUSAQ_REDIS_ADDR configured", false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{
				"PUSAQ_API_KEYS":            "sk-a:team-a,sk-b:team-b",
				"PUSAQ_UPSTREAMS":           "primary=" + upstreamURL(t, &fakeUpstream{}),
				"PUSAQ_RPM_LIMIT":           "1",
				"PUSAQ_RPM_BURST":           "2",
				"PUSAQ_TPM_LIMIT":           "6000",
				"PUSAQ_TPM_BURST":           "100",
				"PUSAQ_TPM_RESERVE_DEFAULT": "10",
				"PUSAQ_RATELIMIT_MODE":      tt.mode,
				"PUSAQ_LEASE_SIZE":          "1",
			}
			if tt.redis {
				env["PUSAQ_REDIS_ADDR"] = miniredis.RunT(t).Addr()
			}
			a, logs := testApp(t, env)
			if (a.leased != nil) != tt.leased {
				t.Fatalf("lease limiter = %v, want %v", a.leased != nil, tt.leased)
			}
			if len(logs.find(t, tt.wantLog)) != 1 {
				t.Fatalf("no %q log record", tt.wantLog)
			}

			var codes []int
			for range 3 {
				codes = append(codes, complete(a.srv, "sk-a", prompt("pusaq/fast", "hello")).Code)
			}
			wantThird := http.StatusOK
			if tt.wantRPM {
				wantThird = http.StatusTooManyRequests
			}
			if codes[0] != http.StatusOK || codes[1] != http.StatusOK || codes[2] != wantThird {
				t.Fatalf("request statuses = %v, want 200, 200, %d", codes, wantThird)
			}

			big := complete(a.srv, "sk-b", `{"model":"pusaq/fast","max_tokens":1000,"messages":[{"role":"user","content":"hello"}]}`)
			wantBig := http.StatusOK
			if tt.redis {
				wantBig = http.StatusBadRequest
			}
			if big.Code != wantBig {
				t.Fatalf("request above the token burst = %d %s, want %d", big.Code, big.Body.String(), wantBig)
			}

			a.close()
			if tt.wantStop != "" && len(logs.find(t, tt.wantStop)) != 1 {
				t.Fatalf("no %q log record at close", tt.wantStop)
			}
		})
	}
}

func TestWiringRoutesThroughBreakersAndFallback(t *testing.T) {
	primary, backup := &fakeUpstream{status: http.StatusInternalServerError}, &fakeUpstream{}
	a, logs := testApp(t, map[string]string{
		"PUSAQ_DEV_MODE":            "true",
		"PUSAQ_UPSTREAMS":           "primary=" + upstreamURL(t, primary) + ",backup=" + upstreamURL(t, backup),
		"PUSAQ_ROUTES":              "pusaq/fast=model-fast,pusaq/best=model-best",
		"PUSAQ_BREAKER_WINDOW":      "4",
		"PUSAQ_BREAKER_MIN_SAMPLES": "2",
		"PUSAQ_BREAKER_THRESHOLD":   "0.5",
		"PUSAQ_BREAKER_COOLDOWN":    "1h",
	})
	if len(a.breakers) != 2 || a.breakers["primary"] == nil || a.breakers["backup"] == nil {
		t.Fatalf("breakers = %v, want one per upstream", a.breakers)
	}

	body := `{"model":"pusaq/auto","metadata":{"feature_tag":"reason"},"messages":[{"role":"user","content":"why"}]}`
	for i := range 3 {
		rec := complete(a.srv, "", body)
		if rec.Code != http.StatusOK || rec.Header().Get(gateway.ServedModelHeader) != "model-best" || rec.Header().Get(gateway.RouteReasonHeader) != "rule:reason" {
			t.Fatalf("request %d = %d, model %q, reason %q", i, rec.Code, rec.Header().Get(gateway.ServedModelHeader), rec.Header().Get(gateway.RouteReasonHeader))
		}
	}
	if primary.hits.Load() != 2 || backup.hits.Load() != 3 {
		t.Fatalf("hits = primary %d backup %d, want 2 and 3: the open breaker skips the primary", primary.hits.Load(), backup.hits.Load())
	}
	transitions := logs.find(t, "breaker_state")
	if len(transitions) != 1 || transitions[0]["backend"] != "primary" || transitions[0]["from"] != "closed" || transitions[0]["to"] != "open" {
		t.Fatalf("breaker_state records = %v, want primary closed→open", transitions)
	}
}

func TestWiringShadowCacheCaps(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantEntries int
		wantSkipped uint64
		wantDropped uint64
	}{
		{"entry cap", map[string]string{"PUSAQ_CACHE_MAX_ENTRIES": "1"}, 1, 0, 0},
		{"byte cap", map[string]string{"PUSAQ_CACHE_MAX_BYTES": "1"}, 0, 2, 0},
		{"pending byte cap", map[string]string{"PUSAQ_CACHE_QUEUE_MAX_BYTES": "1"}, 0, 0, 2},
		{"defaults", map[string]string{}, 2, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{
				"PUSAQ_DEV_MODE":   "true",
				"PUSAQ_CACHE_MODE": "shadow",
				"PUSAQ_UPSTREAMS":  "primary=" + upstreamURL(t, &fakeUpstream{}),
			}
			for k, v := range tt.env {
				env[k] = v
			}
			a, logs := testApp(t, env)
			if a.shadow == nil || len(logs.find(t, "semantic cache in shadow mode: measuring, never serving")) != 1 {
				t.Fatal("shadow cache not wired")
			}
			for _, content := range []string{"What is the capital of France?", "How do I bake bread?"} {
				if rec := complete(a.srv, "", prompt("pusaq/fast", content)); rec.Code != http.StatusOK {
					t.Fatalf("completion = %d %s", rec.Code, rec.Body.String())
				}
			}
			a.close()
			if got := a.index.Len(); got != tt.wantEntries {
				t.Fatalf("index entries = %d, want %d", got, tt.wantEntries)
			}
			if a.shadow.Skipped() != tt.wantSkipped || a.shadow.Dropped() != tt.wantDropped {
				t.Fatalf("skipped %d dropped %d, want %d and %d", a.shadow.Skipped(), a.shadow.Dropped(), tt.wantSkipped, tt.wantDropped)
			}
		})
	}
}

func TestWiringTelemetryPoliciesAndPrices(t *testing.T) {
	a, logs := testApp(t, map[string]string{
		"PUSAQ_API_KEYS":            "sk-a:team-a,sk-b:team-b",
		"PUSAQ_REGION":              "eu-west1",
		"PUSAQ_UPSTREAMS":           "primary=" + upstreamURL(t, &fakeUpstream{}),
		"PUSAQ_ROUTES":              "pusaq/fast=model-fast,pusaq/best=model-best",
		"PUSAQ_REDIS_ADDR":          miniredis.RunT(t).Addr(),
		"PUSAQ_TPM_LIMIT":           "0",
		"PUSAQ_PRICES":              "model-fast=1000000/1000000,model-best=1000000/1000000",
		"PUSAQ_PRICE_TABLE_VERSION": "v-test",
		"PUSAQ_TENANT_POLICIES":     "team-a:models=pusaq/fast;team-b:budget_usd=1",
	})
	for _, msg := range []string{"tenant policies loaded", "price table loaded", "tenant budgets enabled: monthly spend counted in the store", "token limiting disabled: PUSAQ_TPM_LIMIT is 0"} {
		if len(logs.find(t, msg)) != 1 {
			t.Fatalf("no %q log record", msg)
		}
	}

	steps := []struct {
		key, model string
		want       int
	}{
		{"sk-a", "pusaq/fast", http.StatusOK},
		{"sk-a", "pusaq/best", http.StatusForbidden},
		{"sk-b", "pusaq/best", http.StatusOK},
		{"sk-b", "pusaq/best", http.StatusPaymentRequired},
	}
	for i, s := range steps {
		if rec := complete(a.srv, s.key, prompt(s.model, "hello")); rec.Code != s.want {
			t.Fatalf("step %d (%s %s) = %d %s, want %d", i, s.key, s.model, rec.Code, rec.Body.String(), s.want)
		}
	}

	a.close()
	events := logs.find(t, "cost_event")
	if len(events) != len(steps) {
		t.Fatalf("cost events = %d, want %d", len(events), len(steps))
	}
	for i, e := range events {
		if e["region"] != "eu-west1" {
			t.Fatalf("event %d region = %v, want eu-west1", i, e["region"])
		}
	}
	if events[0]["tenant"] != "team-a" || events[0]["price_table_version"] != "v-test" || events[0]["cost_usd"] != 5.0 {
		t.Fatalf("first event = %v, want team-a priced at 5 USD with table v-test", events[0])
	}
	if events[1]["error_code"] != "policy_denied" || events[3]["error_code"] != "budget_exceeded" {
		t.Fatalf("error codes = %v / %v, want policy_denied and budget_exceeded", events[1]["error_code"], events[3]["error_code"])
	}
}

func TestSIGTERMDrainsInFlightRequest(t *testing.T) {
	upstream := &fakeUpstream{arrived: make(chan struct{}, 1), release: make(chan struct{})}
	url := upstreamURL(t, upstream)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(upstream.release) }) }
	t.Cleanup(release)
	a, logs := testApp(t, map[string]string{
		"PUSAQ_DEV_MODE":       "true",
		"PUSAQ_UPSTREAMS":      "primary=" + url,
		"PUSAQ_DRAIN_DELAY":    "500ms",
		"PUSAQ_SHUTDOWN_GRACE": "10s",
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	probe := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: time.Second}
	readyz := func() (int, error) {
		resp, err := probe.Get(base + "/readyz")
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	served := make(chan error, 1)
	go func() { served <- a.serve(ctx, stop, ln) }()
	waitStatus := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if code, _ := readyz(); code == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("readyz never answered %d", want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitStatus(http.StatusOK)

	inFlight := make(chan int, 1)
	go func() {
		resp, err := http.Post(base+"/v1/chat/completions", "application/json", strings.NewReader(prompt("pusaq/fast", "hello")))
		if err != nil {
			inFlight <- 0
			return
		}
		_ = resp.Body.Close()
		inFlight <- resp.StatusCode
	}()
	select {
	case <-upstream.arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the upstream")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitStatus(http.StatusServiceUnavailable)
	logs.waitFor(t, "draining")

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := readyz(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting after the drain delay")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-served:
		t.Fatalf("serve returned %v while a request was in flight", err)
	case code := <-inFlight:
		t.Fatalf("in-flight request ended with %d before its upstream answered", code)
	default:
	}

	release()
	select {
	case code := <-inFlight:
		if code != http.StatusOK {
			t.Fatalf("in-flight request = %d, want 200", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request did not finish after its upstream answered")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the in-flight request finished")
	}
	if len(logs.find(t, "gateway stopped")) == 0 || len(logs.find(t, "cost_event")) != 1 {
		t.Fatal("shutdown did not stop the gateway after flushing the request's cost event")
	}
}

func TestRunServesUntilSIGTERM(t *testing.T) {
	logs := &logBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- run(slog.New(slog.NewJSONHandler(logs, nil)), lookupFrom(map[string]string{
			"PUSAQ_DEV_MODE":    "true",
			"PUSAQ_ADDR":        "127.0.0.1:0",
			"PUSAQ_DRAIN_DELAY": "0s",
		}))
	}()
	logs.waitFor(t, "gateway listening")
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v, want nil after a drain", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after SIGTERM")
	}
	logs.waitFor(t, "gateway stopped")
}

func TestRunReportsStartupErrors(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"invalid configuration", map[string]string{}},
		{"address in use", map[string]string{"PUSAQ_DEV_MODE": "true", "PUSAQ_ADDR": taken.Addr().String()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(slog.New(slog.NewJSONHandler(&logBuffer{}, nil)), lookupFrom(tt.env))
			if err == nil {
				t.Fatal("run succeeded")
			}
			var opErr *net.OpError
			if tt.name == "address in use" && !errors.As(err, &opErr) {
				t.Fatalf("run = %v, want the listen error", err)
			}
		})
	}
}
