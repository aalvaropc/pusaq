package storegate

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/aalvaropc/pusaq/internal/breaker"
)

var errRefused = errors.New("dial tcp 10.0.0.1:6379: connect: connection refused")

type replyError string

func (e replyError) Error() string { return string(e) }
func (replyError) RedisError()     {}

type fakeStore struct {
	mu    sync.Mutex
	calls int
	err   error
	hang  bool
}

func (s *fakeStore) set(err error, hang bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err, s.hang = err, hang
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *fakeStore) process(ctx context.Context, _ redis.Cmder) error {
	s.mu.Lock()
	s.calls++
	err, hang := s.err, s.hang
	s.mu.Unlock()
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

type harness struct {
	g           *Gate
	store       *fakeStore
	hook        redis.ProcessHook
	now         time.Time
	mu          sync.Mutex
	transitions []string
	probes      []func()
	pings       int
	pingErr     error
	pingCtx     context.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{store: &fakeStore{}, now: time.UnixMilli(1_700_000_000_000)}
	h.g = New(func(ctx context.Context) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.pings++
		h.pingCtx = ctx
		return h.pingErr
	}, func(from, to breaker.State) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.transitions = append(h.transitions, string(from)+">"+string(to))
	})
	h.g.b.SetClock(func() time.Time {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.now
	})
	h.g.spawn = func(f func()) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.probes = append(h.probes, f)
	}
	h.hook = h.g.ProcessHook(h.store.process)
	return h
}

func (h *harness) call(ctx context.Context) (redis.Cmder, error) {
	cmd := redis.NewStatusCmd(ctx, "evalsha")
	return cmd, h.hook(ctx, cmd)
}

func (h *harness) calls(t *testing.T, n int) {
	t.Helper()
	for range n {
		_, _ = h.call(context.Background())
	}
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *harness) runProbes() int {
	h.mu.Lock()
	probes := h.probes
	h.probes = nil
	h.mu.Unlock()
	for _, p := range probes {
		p()
	}
	return len(probes)
}

func (h *harness) log() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.transitions...)
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (h *harness) mustSkip(t *testing.T) {
	t.Helper()
	before := h.store.count()
	cmd, err := h.call(context.Background())
	if !errors.Is(err, ErrOpen) || !errors.Is(cmd.Err(), ErrOpen) {
		t.Fatalf("call error = %v, command error = %v, want ErrOpen", err, cmd.Err())
	}
	if h.store.count() != before {
		t.Fatal("a skipped call reached the store")
	}
}

func (h *harness) mustReach(t *testing.T) {
	t.Helper()
	before := h.store.count()
	if _, err := h.call(context.Background()); errors.Is(err, ErrOpen) {
		t.Fatal("call skipped, want it sent to the store")
	}
	if h.store.count() != before+1 {
		t.Fatal("call did not reach the store")
	}
}

func TestOpensOnTheEleventhUnansweredCallOfTheWindow(t *testing.T) {
	h := newHarness(t)
	h.calls(t, Window)
	h.store.set(errRefused, false)
	h.calls(t, 10)
	if got := h.g.State(); got != breaker.StateClosed {
		t.Fatalf("state after 10 of 20 unanswered = %s, want closed", got)
	}
	h.calls(t, 1)
	if got := h.g.State(); got != breaker.StateOpen {
		t.Fatalf("state after 11 of 20 unanswered = %s, want open", got)
	}
	if got := h.log(); !equal(got, []string{"closed>open"}) {
		t.Fatalf("transitions = %v", got)
	}
	h.mustSkip(t)
	if h.runProbes() != 0 {
		t.Fatal("probe started before the cool-down")
	}
}

func TestMinimumSampleBeforeOpening(t *testing.T) {
	h := newHarness(t)
	h.store.set(errRefused, false)
	h.calls(t, MinSamples-1)
	if got := h.g.State(); got != breaker.StateClosed {
		t.Fatalf("state after %d unanswered calls = %s, want closed", MinSamples-1, got)
	}
	h.mustReach(t)
	if got := h.g.State(); got != breaker.StateOpen {
		t.Fatalf("state after %d unanswered calls = %s, want open", MinSamples, got)
	}
}

func TestDeadlineBoundCallsCountAsUnanswered(t *testing.T) {
	h := newHarness(t)
	h.store.set(nil, true)
	for range MinSamples {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		_, err := h.call(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("hung call error = %v, want deadline", err)
		}
	}
	if got := h.g.State(); got != breaker.StateOpen {
		t.Fatalf("state = %s, want open after deadline-bound calls", got)
	}
}

func TestRepliesAndCancellationsDoNotOpen(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"nil reply", redis.Nil},
		{"error reply", replyError("ERR wrong number of arguments")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.store.set(tt.err, false)
			h.calls(t, 3*Window)
			if got := h.g.State(); got != breaker.StateClosed {
				t.Fatalf("state = %s, want closed: the store answered", got)
			}
		})
	}
	t.Run("caller canceled", func(t *testing.T) {
		h := newHarness(t)
		h.store.set(nil, true)
		for range 3 * Window {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = h.call(ctx)
		}
		if got := h.g.State(); got != breaker.StateClosed {
			t.Fatalf("state = %s, want closed: canceled calls count as nothing", got)
		}
		h.store.set(errRefused, false)
		h.calls(t, MinSamples)
		if got := h.g.State(); got != breaker.StateOpen {
			t.Fatalf("state = %s, want open: canceled calls must not fill the window", got)
		}
	})
}

func TestAnsweredProbeCloses(t *testing.T) {
	h := newHarness(t)
	h.store.set(errRefused, false)
	h.calls(t, MinSamples)
	h.advance(Cooldown - time.Millisecond)
	h.mustSkip(t)
	if h.runProbes() != 0 {
		t.Fatal("probe started before the cool-down elapsed")
	}
	h.advance(time.Millisecond)
	h.mustSkip(t)
	h.mustSkip(t)
	if got := h.g.State(); got != breaker.StateHalfOpen {
		t.Fatalf("state = %s, want half_open while the probe runs", got)
	}
	h.store.set(nil, false)
	if n := h.runProbes(); n != 1 {
		t.Fatalf("probes = %d, want one per half-open period", n)
	}
	if h.pingCtx.Value(passKey{}) == nil {
		t.Fatal("probe context would be gated by its own hook")
	}
	if dl, ok := h.pingCtx.Deadline(); !ok || time.Until(dl) > ProbeBudget {
		t.Fatalf("probe deadline = %v (set %t), want at most %s", dl, ok, ProbeBudget)
	}
	if got := h.log(); !equal(got, []string{"closed>open", "open>half_open", "half_open>closed"}) {
		t.Fatalf("transitions = %v", got)
	}
	h.store.set(errRefused, false)
	h.calls(t, MinSamples-1)
	if got := h.g.State(); got != breaker.StateClosed {
		t.Fatalf("state = %s, want closed: recovery starts an empty window", got)
	}
	h.mustReach(t)
}

func TestUnansweredProbeReopens(t *testing.T) {
	h := newHarness(t)
	h.store.set(errRefused, false)
	h.calls(t, MinSamples)
	h.advance(Cooldown)
	h.mustSkip(t)
	h.pingErr = context.DeadlineExceeded
	h.runProbes()
	if got := h.g.State(); got != breaker.StateOpen {
		t.Fatalf("state = %s, want open after an unanswered probe", got)
	}
	h.mustSkip(t)
	if h.runProbes() != 0 {
		t.Fatal("probe started before the new cool-down")
	}
	h.advance(Cooldown)
	h.mustSkip(t)
	h.pingErr = replyError("LOADING Redis is loading the dataset in memory")
	if h.runProbes() != 1 || h.g.State() != breaker.StateClosed {
		t.Fatalf("state = %s, want closed after a probe the store answered", h.g.State())
	}
	want := []string{"closed>open", "open>half_open", "half_open>open", "open>half_open", "half_open>closed"}
	if got := h.log(); !equal(got, want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
}

func TestStragglerDoesNotDecideTheProbe(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	slow := h.g.ProcessHook(func(context.Context, redis.Cmder) error {
		close(entered)
		<-release
		return nil
	})
	done := make(chan error)
	go func() { done <- slow(context.Background(), redis.NewStatusCmd(context.Background(), "evalsha")) }()
	<-entered
	h.store.set(errRefused, false)
	h.calls(t, MinSamples)
	h.advance(Cooldown)
	h.mustSkip(t)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("straggler error = %v", err)
	}
	if got := h.g.State(); got != breaker.StateHalfOpen {
		t.Fatalf("state = %s, want half_open: a straggler's success must not close the gate", got)
	}
	h.pingErr = errRefused
	h.runProbes()
	if got := h.g.State(); got != breaker.StateOpen {
		t.Fatalf("state = %s, want open: the probe alone decides", got)
	}
}

func TestHealthyStoreSeesNoExtraCall(t *testing.T) {
	h := newHarness(t)
	for i := range 5 * Window {
		h.mustReach(t)
		if i%Window == 0 {
			h.advance(time.Hour)
		}
	}
	if h.store.count() != 5*Window || h.pings != 0 || len(h.log()) != 0 || h.runProbes() != 0 {
		t.Fatalf("store calls %d, pings %d, transitions %v", h.store.count(), h.pings, h.log())
	}
}

func TestNestedCallsCountOnce(t *testing.T) {
	h := newHarness(t)
	var hook redis.ProcessHook
	hook = h.g.ProcessHook(func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "evalsha" {
			if err := hook(ctx, redis.NewStatusCmd(ctx, "hello")); err != nil {
				return err
			}
		}
		return errRefused
	})
	for range MinSamples - 1 {
		_ = hook(context.Background(), redis.NewStatusCmd(context.Background(), "evalsha"))
	}
	if got := h.g.State(); got != breaker.StateClosed {
		t.Fatalf("state = %s after %d calls with a nested setup command, want closed", got, MinSamples-1)
	}
}

func TestPipelinesAreGated(t *testing.T) {
	h := newHarness(t)
	sent := 0
	pipe := h.g.ProcessPipelineHook(func(context.Context, []redis.Cmder) error {
		sent++
		return errRefused
	})
	cmds := func() []redis.Cmder {
		return []redis.Cmder{redis.NewStatusCmd(context.Background(), "get"), redis.NewStatusCmd(context.Background(), "set")}
	}
	for range MinSamples {
		_ = pipe(context.Background(), cmds())
	}
	skipped := cmds()
	if err := pipe(context.Background(), skipped); !errors.Is(err, ErrOpen) || sent != MinSamples {
		t.Fatalf("error = %v after %d pipelines sent, want ErrOpen after %d", err, sent, MinSamples)
	}
	for _, cmd := range skipped {
		if !errors.Is(cmd.Err(), ErrOpen) {
			t.Fatalf("command %s error = %v, want ErrOpen", cmd.Name(), cmd.Err())
		}
	}
}

func TestConcurrentCallsShareOneProbe(t *testing.T) {
	h := newHarness(t)
	var probes sync.WaitGroup
	var pings atomic.Int32
	h.g.ping = func(context.Context) error {
		pings.Add(1)
		return nil
	}
	h.g.spawn = func(f func()) {
		probes.Go(f)
	}
	h.store.set(errRefused, false)
	burst := func() {
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				for range 50 {
					_, _ = h.call(context.Background())
				}
			})
		}
		wg.Wait()
	}
	burst()
	if got := h.g.State(); got != breaker.StateOpen {
		t.Fatalf("state = %s, want open after a failing burst", got)
	}
	h.store.set(nil, false)
	h.advance(Cooldown)
	burst()
	probes.Wait()
	if pings.Load() != 1 || h.g.State() != breaker.StateClosed {
		t.Fatalf("pings = %d, state = %s; want one probe that closes the gate", pings.Load(), h.g.State())
	}
}

func TestAttachProbesThroughTheClient(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	var transitions []string
	g := Attach(client, func(from, to breaker.State) { transitions = append(transitions, string(from)+">"+string(to)) })
	now := time.UnixMilli(1_700_000_000_000)
	g.b.SetClock(func() time.Time { return now })
	g.spawn = func(f func()) { f() }

	ctx := context.Background()
	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("healthy call: %v", err)
	}
	addr := mr.Addr()
	mr.Close()
	for range MinSamples {
		callCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		_ = client.Get(callCtx, "k").Err()
		cancel()
	}
	if got := g.State(); got != breaker.StateOpen {
		t.Fatalf("state = %s with the store gone, want open", got)
	}
	if err := client.Get(ctx, "k").Err(); !errors.Is(err, ErrOpen) {
		t.Fatalf("call error = %v, want ErrOpen", err)
	}
	if err := mr.StartAddr(addr); err != nil {
		t.Fatalf("restart store: %v", err)
	}
	now = now.Add(Cooldown)
	if err := client.Get(ctx, "k").Err(); !errors.Is(err, ErrOpen) {
		t.Fatalf("call that starts the probe: error = %v, want ErrOpen", err)
	}
	if got := g.State(); got != breaker.StateClosed {
		t.Fatalf("state = %s after the store answered the PING, want closed", got)
	}
	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("call after recovery: %v", err)
	}
	if !equal(transitions, []string{"closed>open", "open>half_open", "half_open>closed"}) {
		t.Fatalf("transitions = %v", transitions)
	}
}
