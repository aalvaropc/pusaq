package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/retry"
)

type fakeClock struct {
	mu    sync.Mutex
	t     time.Time
	waits []time.Duration
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Now()} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	c.t = c.t.Add(d)
	return nil
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func testRetries(maxRetries int, window time.Duration, budget *retry.Budget, c *fakeClock) Retries {
	return Retries{Max: maxRetries, Window: window, Budget: budget, now: c.now, sleep: c.sleep, jitter: func() float64 { return 0.5 }}
}

func retryable(status int, after time.Duration) error {
	return &core.Error{Code: core.CodeUpstream, Message: "upstream provider returned status " + strconv.Itoa(status),
		Err: &provider.RetryableError{Status: status, RetryAfter: after}}
}

type flakyProvider struct {
	name   string
	fails  []error
	always error
	calls  int
	keys   []string
}

func (f *flakyProvider) Name() string { return f.name }

func (f *flakyProvider) outcome(req provider.ChatRequest) error {
	f.calls++
	f.keys = append(f.keys, req.IdempotencyKey)
	if len(f.fails) > 0 {
		err := f.fails[0]
		f.fails = f.fails[1:]
		return err
	}
	return f.always
}

func (f *flakyProvider) Complete(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if err := f.outcome(req); err != nil {
		return provider.ChatResponse{}, err
	}
	return provider.ChatResponse{
		ID: "cmpl-" + f.name, Model: req.Model,
		Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: "from " + f.name}, FinishReason: "stop"}},
		Usage:   core.Usage{PromptTokens: 3, CompletionTokens: 2},
	}, nil
}

func (f *flakyProvider) StreamComplete(_ context.Context, req provider.ChatRequest) (io.ReadCloser, error) {
	if err := f.outcome(req); err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(fakeSSE)), nil
}

func retryServer(primary, backup provider.Provider, opts CompletionsOptions) (*Server, *[]core.CostEvent) {
	var mu sync.Mutex
	var events []core.CostEvent
	if opts.Logger == nil {
		opts.Logger = discardLogger()
	}
	pipeline := core.Chain(Accounting(func(e core.CostEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}, discardLogger()), RequestID(), Auth(nil))
	h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{"primary": primary, "backup": backup}, opts)
	return NewServer(discardLogger(), pipeline, h), &events
}

func TestEachRetryableClassIsRetriedOnTheSameBackend(t *testing.T) {
	classes := map[string]func(w http.ResponseWriter){
		"reset": func(w http.ResponseWriter) {
			if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
				_ = conn.Close()
			}
		},
		"header timeout": func(http.ResponseWriter) { time.Sleep(300 * time.Millisecond) },
	}
	for _, status := range []int{408, 429, 500, 502, 503, 504} {
		classes[strconv.Itoa(status)] = func(w http.ResponseWriter) { w.WriteHeader(status) }
	}
	for name, fail := range classes {
		for _, payload := range [][]byte{validPayload(), streamingPayload()} {
			stream := strings.Contains(string(payload), `"stream":true`)
			t.Run(name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				var mu sync.Mutex
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					mu.Lock()
					calls++
					first := calls == 1
					mu.Unlock()
					if first {
						fail(w)
						return
					}
					if stream {
						_, _ = io.WriteString(w, fakeSSE)
						return
					}
					_, _ = io.WriteString(w, `{"id":"c","model":"mock-model","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
				}))
				defer upstream.Close()
				backup := &flakyProvider{name: "backup"}
				c := newFakeClock()
				s, events := retryServer(provider.NewHTTP("primary", upstream.URL, 100*time.Millisecond, discardLogger()), backup,
					CompletionsOptions{Retries: testRetries(2, time.Minute, retry.NewBudget(0.2), c)})

				rec := post(s, string(payload))
				if rec.Code != http.StatusOK || backup.calls != 0 || calls != 2 {
					t.Fatalf("status = %d primary calls = %d backup calls = %d, want 200 from the primary's retry", rec.Code, calls, backup.calls)
				}
				if e := (*events)[0]; e.ServedProvider != "primary" || e.Attempts != 2 || e.Retries != 1 || len(e.RetryDenied) != 0 {
					t.Fatalf("event = %+v", e)
				}
				if len(c.waits) != 1 || c.waits[0] != 100*time.Millisecond {
					t.Fatalf("waits = %v, want one full-jitter wait of 100ms (u=0.5, first ceiling 200ms)", c.waits)
				}
			})
		}
	}
}

func TestRejectionsOtherFailuresAndCancellationAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		status       int
		body         string
		wantCode     int
		wantBackup   int
		wantAttempts int
	}{
		{400, `{}`, http.StatusBadRequest, 0, 1},
		{413, `{}`, http.StatusBadRequest, 0, 1},
		{422, `{}`, http.StatusBadRequest, 0, 1},
		{401, `{}`, http.StatusOK, 1, 2},
		{404, `{}`, http.StatusOK, 1, 2},
		{501, `{}`, http.StatusOK, 1, 2},
		{200, `{"choices":[]}`, http.StatusOK, 1, 2},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			backup := &flakyProvider{name: "backup"}
			c := newFakeClock()
			s, events := retryServer(provider.NewHTTP("primary", upstream.URL, time.Second, discardLogger()), backup,
				CompletionsOptions{Retries: testRetries(3, time.Minute, nil, c)})

			rec := post(s, string(validPayload()))
			e := (*events)[0]
			if rec.Code != tc.wantCode || calls != 1 || backup.calls != tc.wantBackup || e.Attempts != tc.wantAttempts || e.Retries != 0 || len(c.waits) != 0 {
				t.Fatalf("status = %d primary = %d backup = %d waits = %v event = %+v", rec.Code, calls, backup.calls, c.waits, e)
			}
		})
	}

	for name, payload := range map[string][]byte{"buffered": validPayload(), "stream": streamingPayload()} {
		t.Run("canceled during the attempt/"+name, func(t *testing.T) {
			primary := &blockingProvider{name: "primary"}
			backup := &flakyProvider{name: "backup"}
			brs, m := tightBreakers(t)
			c := newFakeClock()
			h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{"primary": primary, "backup": backup},
				CompletionsOptions{Breakers: brs, Retries: testRetries(3, time.Minute, nil, c), Logger: discardLogger()})
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(20*time.Millisecond, cancel)
			req := &core.Request{Op: "chat.completions", Payload: payload}
			_, err := h(ctx, req)
			cancel()
			if core.AsError(err).Code != core.CodeClientCanceled || primary.calls != 1 || backup.calls != 0 || req.Retries != 0 || len(c.waits) != 0 {
				t.Fatalf("err = %v primary = %d backup = %d retries = %d waits = %v", err, primary.calls, backup.calls, req.Retries, c.waits)
			}
			if got := m["primary"].State(); got != breaker.StateClosed {
				t.Fatalf("primary breaker = %s, want closed", got)
			}
		})
	}
}

func TestCanceledDuringTheWaitStopsWithoutAnotherAttempt(t *testing.T) {
	primary := &flakyProvider{name: "primary", always: retryable(503, 0)}
	backup := &flakyProvider{name: "backup"}
	budget := retry.NewBudget(0.25)
	c := newFakeClock()
	budget.SetClock(c.now)
	ctx, cancel := context.WithCancel(context.Background())
	opts := CompletionsOptions{Retries: testRetries(2, time.Minute, budget, c), Logger: discardLogger()}
	opts.Retries.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{"primary": primary, "backup": backup}, opts)
	req := &core.Request{Op: "chat.completions", Payload: validPayload()}
	_, err := h(ctx, req)
	if core.AsError(err).Code != core.CodeClientCanceled || primary.calls != 1 || backup.calls != 0 || req.Attempts != 1 || req.Retries != 0 {
		t.Fatalf("err = %v primary = %d backup = %d attempts = %d retries = %d", err, primary.calls, backup.calls, req.Attempts, req.Retries)
	}
	if got := drainBudget(budget); got != int(retry.Reserve) {
		t.Fatalf("budget holds %d retries, want the reserve back: the canceled retry must be refunded", got)
	}
}

func drainBudget(b *retry.Budget) int {
	n := 0
	for b.Withdraw() {
		n++
	}
	return n
}

func TestPerRequestMaximumCountsAcrossTheChain(t *testing.T) {
	for _, tc := range []struct {
		name         string
		backupFails  bool
		maxRetries   int
		wantPrimary  int
		wantBackup   int
		wantStatus   int
		wantRetries  int
		wantAttempts int
		wantWaitsMS  []int64
		wantServedBy string
	}{
		{"primary exhausts the maximum then fallback serves", false, 2, 3, 1, http.StatusOK, 2, 4, []int64{100, 200}, "backup"},
		{"no retries left for the fallback", true, 2, 3, 1, http.StatusBadGateway, 2, 4, []int64{100, 200}, ""},
		{"a larger maximum is spent on the primary first", true, 3, 4, 1, http.StatusBadGateway, 3, 5, []int64{100, 200, 400}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := &flakyProvider{name: "primary", always: retryable(500, 0)}
			backup := &flakyProvider{name: "backup"}
			if tc.backupFails {
				backup.always = retryable(500, 0)
			}
			c := newFakeClock()
			s, events := retryServer(primary, backup, CompletionsOptions{Retries: testRetries(tc.maxRetries, time.Minute, nil, c)})
			rec := post(s, string(validPayload()))
			e := (*events)[0]
			var waits []int64
			for _, w := range c.waits {
				waits = append(waits, w.Milliseconds())
			}
			if rec.Code != tc.wantStatus || primary.calls != tc.wantPrimary || backup.calls != tc.wantBackup ||
				e.Retries != tc.wantRetries || e.Attempts != tc.wantAttempts || e.ServedProvider != tc.wantServedBy || !slices.Equal(waits, tc.wantWaitsMS) {
				t.Fatalf("status = %d primary = %d backup = %d waits = %v event = %+v", rec.Code, primary.calls, backup.calls, waits, e)
			}
		})
	}
}

func TestBackoffRestartsOnEachBackend(t *testing.T) {
	primary := &flakyProvider{name: "primary", fails: []error{retryable(503, 0), retryable(503, 0), core.NewError(core.CodeUpstream, "upstream provider returned status 404")}}
	backup := &flakyProvider{name: "backup", fails: []error{retryable(503, 0)}}
	c := newFakeClock()
	s, events := retryServer(primary, backup, CompletionsOptions{Retries: testRetries(5, time.Minute, nil, c)})
	if rec := post(s, string(validPayload())); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 100 * time.Millisecond}; !slices.Equal(c.waits, want) {
		t.Fatalf("waits = %v, want %v", c.waits, want)
	}
	if e := (*events)[0]; e.ServedProvider != "backup" || e.Attempts != 5 || e.Retries != 3 {
		t.Fatalf("event = %+v, want backup after 5 attempts of which 3 retries", e)
	}
}

func TestRetryAfterHonoredInsideTheDeadlineOnly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		retryAfter  string
		window      time.Duration
		ctxDeadline time.Duration
		wantWaits   []time.Duration
		wantServed  string
		wantDenied  []string
	}{
		{"fits the window", "1", 10 * time.Second, 0, []time.Duration{time.Second}, "primary", nil},
		{"replaces the computed backoff", "3", 10 * time.Second, 0, []time.Duration{3 * time.Second}, "primary", nil},
		{"beyond the window", "30", 10 * time.Second, 0, nil, "backup", []string{"deadline"}},
		{"ends exactly at the window", "10", 10 * time.Second, 0, nil, "backup", []string{"deadline"}},
		{"beyond the context deadline", "3", time.Minute, 2 * time.Second, nil, "backup", []string{"deadline"}},
		{"absent uses the backoff", "", 10 * time.Second, 0, []time.Duration{100 * time.Millisecond}, "primary", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 1 {
					if tc.retryAfter != "" {
						w.Header().Set("Retry-After", tc.retryAfter)
					}
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = io.WriteString(w, `{"id":"c","model":"mock-model","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer upstream.Close()
			backup := &flakyProvider{name: "backup"}
			c := newFakeClock()
			h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{
				"primary": provider.NewHTTP("primary", upstream.URL, time.Second, discardLogger()), "backup": backup},
				CompletionsOptions{Retries: testRetries(2, tc.window, retry.NewBudget(0.2), c), Logger: discardLogger()})
			ctx := context.Background()
			if tc.ctxDeadline > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, c.now().Add(tc.ctxDeadline))
				defer cancel()
			}
			req := &core.Request{Op: "chat.completions", Payload: validPayload()}
			if _, err := h(ctx, req); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(c.waits, tc.wantWaits) || req.ServedProvider != tc.wantServed || !slices.Equal(req.RetryDenied, tc.wantDenied) {
				t.Fatalf("waits = %v served = %q denied = %v, want %v %q %v", c.waits, req.ServedProvider, req.RetryDenied, tc.wantWaits, tc.wantServed, tc.wantDenied)
			}
		})
	}
}

func TestWindowIsMeasuredFromTheFirstAttempt(t *testing.T) {
	c := newFakeClock()
	primary := &flakyProvider{name: "primary", always: retryable(503, 0)}
	slow := &slowProvider{Provider: primary, clock: c, took: 9 * time.Second}
	backup := &flakyProvider{name: "backup"}
	s, events := retryServer(slow, backup, CompletionsOptions{Retries: testRetries(3, 10*time.Second, nil, c)})
	if rec := post(s, string(validPayload())); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	e := (*events)[0]
	if primary.calls != 2 || e.Retries != 1 || e.ServedProvider != "backup" || !slices.Equal(e.RetryDenied, []string{"deadline"}) {
		t.Fatalf("primary calls = %d event = %+v, want one retry started at 9.1s and none after 18.1s", primary.calls, e)
	}
}

type slowProvider struct {
	provider.Provider
	clock *fakeClock
	took  time.Duration
}

func (s *slowProvider) Complete(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.clock.advance(s.took)
	return s.Provider.Complete(ctx, req)
}

func TestBudgetBoundsAmplificationUnderAFailingUpstream(t *testing.T) {
	const requests = 1000
	run := func(budget *retry.Budget) (primaryCalls, denied int) {
		primary := &flakyProvider{name: "primary", always: retryable(500, 0)}
		backup := &flakyProvider{name: "backup"}
		c := newFakeClock()
		if budget != nil {
			frozen := c.now()
			budget.SetClock(func() time.Time { return frozen })
		}
		h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{"primary": primary, "backup": backup},
			CompletionsOptions{Retries: testRetries(2, time.Minute, budget, c), Logger: discardLogger()})
		for i := range requests {
			req := &core.Request{ID: strconv.Itoa(i), Op: "chat.completions", Payload: validPayload()}
			if _, err := h(context.Background(), req); err != nil || req.ServedProvider != "backup" {
				t.Fatalf("request %d: err = %v served = %q", i, err, req.ServedProvider)
			}
			if slices.Contains(req.RetryDenied, "budget") {
				denied++
			}
		}
		return primary.calls, denied
	}

	unbudgeted, deniedWithout := run(nil)
	budgeted, deniedWith := run(retry.NewBudget(0.2))
	bound := requests + int(retry.Reserve+0.2*requests)
	if unbudgeted != 3*requests || deniedWithout != 0 {
		t.Fatalf("without a budget: %d primary attempts, %d denials; want %d and 0", unbudgeted, deniedWithout, 3*requests)
	}
	if budgeted > bound || budgeted < bound-2 {
		t.Fatalf("with a 0.2 budget: %d primary attempts, want at most %d", budgeted, bound)
	}
	if deniedWith < requests-(bound-requests) {
		t.Fatalf("with a 0.2 budget: %d requests denied a retry, want at least %d", deniedWith, requests-(bound-requests))
	}
	t.Logf("%d requests, primary always 500, max 2 retries: %.2f primary attempts/request without the budget, %.3f with ratio 0.2 (bound %.3f); %d requests denied by the budget",
		requests, float64(unbudgeted)/requests, float64(budgeted)/requests, float64(bound)/requests, deniedWith)
}

func TestBudgetFloorRefillsAcrossIdleTime(t *testing.T) {
	budget := retry.NewBudget(0.2)
	c := newFakeClock()
	budget.SetClock(c.now)
	drainBudget(budget)
	primary := &flakyProvider{name: "primary", always: retryable(503, 0)}
	s, events := retryServer(primary, &flakyProvider{name: "backup"}, CompletionsOptions{Retries: testRetries(1, time.Minute, budget, c)})
	post(s, string(validPayload()))
	c.advance(time.Second)
	post(s, string(validPayload()))
	if first, second := (*events)[0], (*events)[1]; first.Retries != 0 || !slices.Equal(first.RetryDenied, []string{"budget"}) || second.Retries != 1 || second.RetryDenied != nil {
		t.Fatalf("first = %+v second = %+v, want a budget denial then a retry paid by one second of refill", first, second)
	}
}

func TestEveryRetryGoesThroughTheBreaker(t *testing.T) {
	t.Run("failures open the breaker mid-request", func(t *testing.T) {
		primary := &flakyProvider{name: "primary", always: retryable(503, 0)}
		backup := &flakyProvider{name: "backup"}
		brs, m := tightBreakers(t)
		budget := retry.NewBudget(0.25)
		c := newFakeClock()
		budget.SetClock(c.now)
		s, events := retryServer(primary, backup, CompletionsOptions{Breakers: brs, Retries: testRetries(3, time.Minute, budget, c)})
		if rec := post(s, string(validPayload())); rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := m["primary"].State(); got != breaker.StateOpen {
			t.Fatalf("primary breaker = %s, want open: each retry records its own failure", got)
		}
		if e := (*events)[0]; primary.calls != 2 || e.Retries != 1 || e.ServedProvider != "backup" {
			t.Fatalf("primary calls = %d event = %+v, want the open breaker to stop the second retry", primary.calls, e)
		}
		if got := drainBudget(budget); got != int(retry.Reserve)-1 {
			t.Fatalf("budget holds %d, want %d: one retry made, the refused one refunded", got, int(retry.Reserve)-1)
		}
	})

	t.Run("a failed half-open probe stops retries", func(t *testing.T) {
		primary := &flakyProvider{name: "primary", always: retryable(503, 0)}
		backup := &flakyProvider{name: "backup"}
		brs, m := tightBreakers(t)
		now := time.Now()
		m["primary"].SetClock(func() time.Time { return now })
		openBreaker(m["primary"])
		now = now.Add(2 * time.Hour)
		c := newFakeClock()
		s, events := retryServer(primary, backup, CompletionsOptions{Breakers: brs, Retries: testRetries(3, time.Minute, nil, c)})
		post(s, string(validPayload()))
		if e := (*events)[0]; primary.calls != 1 || e.Retries != 0 || e.ServedProvider != "backup" || m["primary"].State() != breaker.StateOpen {
			t.Fatalf("primary calls = %d breaker = %s event = %+v", primary.calls, m["primary"].State(), e)
		}
	})
}

func TestStreamsRetryOnlyWhileOpening(t *testing.T) {
	t.Run("pre-stream failure retried", func(t *testing.T) {
		primary := &flakyProvider{name: "primary", fails: []error{retryable(503, 0)}}
		backup := &flakyProvider{name: "backup"}
		c := newFakeClock()
		s, events := retryServer(primary, backup, CompletionsOptions{Retries: testRetries(2, time.Minute, nil, c)})
		rec := post(s, string(streamingPayload()))
		if rec.Code != http.StatusOK || !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") || backup.calls != 0 {
			t.Fatalf("status = %d backup = %d body = %q", rec.Code, backup.calls, rec.Body.String())
		}
		if e := (*events)[0]; e.ServedProvider != "primary" || e.Attempts != 2 || e.Retries != 1 || e.Outcome != core.OutcomeSuccess {
			t.Fatalf("event = %+v", e)
		}
	})
	t.Run("mid-stream failure not retried", func(t *testing.T) {
		primary := &streamQueue{streams: []io.ReadCloser{brokenStream(), io.NopCloser(strings.NewReader(fakeSSE))}}
		backup := &flakyProvider{name: "backup"}
		c := newFakeClock()
		s, events := retryServer(primary, backup, CompletionsOptions{Retries: testRetries(3, time.Minute, retry.NewBudget(1), c)})
		rec := post(s, string(streamingPayload()))
		if primary.calls != 1 || backup.calls != 0 || len(c.waits) != 0 || !strings.HasSuffix(rec.Body.String(), streamErrorEvent) {
			t.Fatalf("primary = %d backup = %d waits = %v body = %q", primary.calls, backup.calls, c.waits, rec.Body.String())
		}
		if e := (*events)[0]; e.ErrorCode != core.CodeUpstream || e.Attempts != 1 || e.Retries != 0 {
			t.Fatalf("event = %+v", e)
		}
	})
}

func TestOneIdempotencyKeyPerRequestAcrossRetriesAndFallback(t *testing.T) {
	for name, payload := range map[string][]byte{"buffered": validPayload(), "stream": streamingPayload()} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Header.Get("Idempotency-Key"))
				mu.Unlock()
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer upstream.Close()
			backup := &flakyProvider{name: "backup"}
			c := newFakeClock()
			s, events := retryServer(provider.NewHTTP("primary", upstream.URL, time.Second, discardLogger()), backup,
				CompletionsOptions{Retries: testRetries(1, time.Minute, nil, c)})

			var ids []string
			for range 2 {
				rec := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(payload)))
				r.Header.Set("Idempotency-Key", "client-k1")
				s.ServeHTTP(rec, r)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d", rec.Code)
				}
				ids = append(ids, rec.Header().Get(RequestIDHeader))
			}
			if ids[0] == "" || ids[0] == ids[1] {
				t.Fatalf("request IDs = %v", ids)
			}
			wantPrimary := []string{"pusaq-" + ids[0], "pusaq-" + ids[0], "pusaq-" + ids[1], "pusaq-" + ids[1]}
			wantBackup := []string{"pusaq-" + ids[0], "pusaq-" + ids[1]}
			if !slices.Equal(seen, wantPrimary) || !slices.Equal(backup.keys, wantBackup) {
				t.Fatalf("primary keys = %v backup keys = %v, want %v and %v", seen, backup.keys, wantPrimary, wantBackup)
			}
			for _, e := range *events {
				if e.Attempts != 3 || e.Retries != 1 || e.ServedProvider != "backup" {
					t.Fatalf("event = %+v", e)
				}
			}
		})
	}
}

func TestRetriesChargeTokensOnceAndReconcileOnce(t *testing.T) {
	for _, tc := range []struct {
		name        string
		primary     *flakyProvider
		backup      *flakyProvider
		wantAdjusts []float64
		wantStatus  int
	}{
		{"retried then served", &flakyProvider{name: "primary", fails: []error{retryable(503, 0), retryable(500, 0)}}, &flakyProvider{name: "backup"}, []float64{30}, http.StatusOK},
		{"retries and fallback all fail", &flakyProvider{name: "primary", always: retryable(503, 0)}, &flakyProvider{name: "backup", always: retryable(503, 0)}, nil, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				l := &settlementTokenLimiter{fakeTokenLimiter: fakeTokenLimiter{allowed: true}}
				p, b := *tc.primary, *tc.backup
				c := newFakeClock()
				s, events := retryServer(&p, &b, CompletionsOptions{Tokens: l, ReserveDefault: 100, Retries: testRetries(2, time.Minute, nil, c)})
				body := strings.TrimSuffix(string(validPayload()), "}") + `,"stream":` + strconv.FormatBool(stream) + `}`
				rec := post(s, body)
				if stream && rec.Code == http.StatusOK {
					tc.wantAdjusts = []float64{35 - 13}
				}
				e := (*events)[0]
				if rec.Code != tc.wantStatus || len(l.charges) != 1 || l.charges[0] != 35 || !slices.Equal(l.adjusts, tc.wantAdjusts) || e.Retries != 2 || e.TokensCharged != 35 {
					t.Fatalf("stream=%t: status = %d charges = %v adjusts = %v event = %+v", stream, rec.Code, l.charges, l.adjusts, e)
				}
			}
		})
	}
}

func TestRetriesOffByDefault(t *testing.T) {
	primary := &flakyProvider{name: "primary", always: retryable(503, time.Second)}
	backup := &flakyProvider{name: "backup", always: retryable(503, time.Second)}
	s, events := retryServer(primary, backup, CompletionsOptions{})
	if rec := post(s, string(validPayload())); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := (*events)[0]; primary.calls != 1 || backup.calls != 1 || e.Attempts != 2 || e.Retries != 0 || e.RetryDenied != nil {
		t.Fatalf("primary = %d backup = %d event = %+v", primary.calls, backup.calls, e)
	}
}

func TestBudgetUntouchedWithRetriesOff(t *testing.T) {
	budget := retry.NewBudget(1)
	c := newFakeClock()
	budget.SetClock(c.now)
	drainBudget(budget)
	primary := &flakyProvider{name: "primary", always: retryable(503, 0)}
	s, _ := retryServer(primary, &flakyProvider{name: "backup"}, CompletionsOptions{Retries: testRetries(0, time.Minute, budget, c)})
	for range 5 {
		post(s, string(validPayload()))
	}
	if got := drainBudget(budget); got != 0 || primary.calls != 5 {
		t.Fatalf("budget credited %d retries with retries off, primary calls = %d", got, primary.calls)
	}
}
