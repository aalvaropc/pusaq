package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/retry"
)

func withdrawals(b *retry.Budget) int {
	n := 0
	for b.Withdraw() {
		n++
	}
	return n
}

func retryEnv(t *testing.T, primary, backup http.Handler, extra map[string]string) map[string]string {
	t.Helper()
	p, b := httptest.NewServer(primary), httptest.NewServer(backup)
	t.Cleanup(p.Close)
	t.Cleanup(b.Close)
	env := map[string]string{
		"PUSAQ_DEV_MODE":  "true",
		"PUSAQ_UPSTREAMS": "primary=" + p.URL + "/v1,backup=" + b.URL + "/v1",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func costEvent(t *testing.T, logs *logBuffer) map[string]any {
	t.Helper()
	logs.waitFor(t, "cost_event")
	events := logs.find(t, "cost_event")
	if len(events) != 1 {
		t.Fatalf("cost events = %d, want 1", len(events))
	}
	return events[0]
}

func TestWiringRetriesOffByDefault(t *testing.T) {
	primary, backup := &fakeUpstream{status: http.StatusServiceUnavailable}, &fakeUpstream{}
	a, logs := testApp(t, retryEnv(t, primary, backup, nil))
	if a.retries.Max != 0 || a.retries.Budget != nil {
		t.Fatalf("retries = %+v, want off", a.retries)
	}
	if rec := complete(a.srv, "", prompt("pusaq/fast", "hello")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if primary.hits.Load() != 1 || backup.hits.Load() != 1 {
		t.Fatalf("hits = primary %d backup %d, want one attempt each", primary.hits.Load(), backup.hits.Load())
	}
	if e := costEvent(t, logs); e["attempts"] != 2.0 || e["retries"] != 0.0 || e["retry_denied"] != nil {
		t.Fatalf("cost event = %v", e)
	}
	got := logs.find(t, "upstream retries configured")
	if len(got) != 1 || got[0]["max"] != 0.0 || got[0]["budget_ratio"] != 0.2 || got[0]["window"] != float64(10*time.Second) {
		t.Fatalf("startup records = %v, want max 0, ratio 0.2, window 10s", got)
	}
}

func TestWiringRetrySettingsReachTheHandler(t *testing.T) {
	primary, backup := &fakeUpstream{status: http.StatusServiceUnavailable}, &fakeUpstream{}
	a, logs := testApp(t, retryEnv(t, primary, backup, map[string]string{
		"PUSAQ_RETRY_MAX":          "2",
		"PUSAQ_RETRY_BUDGET_RATIO": "0.5",
		"PUSAQ_RETRY_WINDOW":       "1m",
	}))
	if a.retries.Max != 2 || a.retries.Window != time.Minute || a.retries.Budget == nil {
		t.Fatalf("retries = %+v", a.retries)
	}
	if rec := complete(a.srv, "", prompt("pusaq/fast", "hello")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if primary.hits.Load() != 3 || backup.hits.Load() != 1 {
		t.Fatalf("hits = primary %d backup %d, want 3 and 1", primary.hits.Load(), backup.hits.Load())
	}
	if e := costEvent(t, logs); e["attempts"] != 4.0 || e["retries"] != 2.0 || e["served_provider"] != "backup" {
		t.Fatalf("cost event = %v", e)
	}
	withdrawals(a.retries.Budget)
	for range 4 {
		a.retries.Budget.Deposit()
	}
	if got := withdrawals(a.retries.Budget); got != 2 {
		t.Fatalf("four credits paid %d retries, want 2 at ratio 0.5", got)
	}
	got := logs.find(t, "upstream retries configured")
	if len(got) != 1 || got[0]["max"] != 2.0 || got[0]["budget_ratio"] != 0.5 || got[0]["window"] != float64(time.Minute) {
		t.Fatalf("startup records = %v", got)
	}
}

func TestWiringRetryWindowBoundsRetryAfter(t *testing.T) {
	var hits atomic.Int32
	primary := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	a, logs := testApp(t, retryEnv(t, primary, &fakeUpstream{}, map[string]string{
		"PUSAQ_RETRY_MAX":    "1",
		"PUSAQ_RETRY_WINDOW": "500ms",
	}))
	start := time.Now()
	if rec := complete(a.srv, "", prompt("pusaq/fast", "hello")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if hits.Load() != 1 || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("primary hits = %d after %v, want one attempt and no wait", hits.Load(), time.Since(start))
	}
	if e := costEvent(t, logs); e["retries"] != 0.0 || e["retry_denied"] != "deadline" {
		t.Fatalf("cost event = %v", e)
	}
}
