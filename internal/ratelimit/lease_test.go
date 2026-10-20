package ratelimit

import (
	"context"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// leaseFixture is one shared miniredis with a frozen clock every limiter
// built from it shares, so refill is under the test's control.
type leaseFixture struct {
	t   *testing.T
	mr  *miniredis.Miniredis
	now time.Time
	mu  sync.Mutex
}

func newLeaseFixture(t *testing.T) *leaseFixture {
	t.Helper()
	f := &leaseFixture{t: t, mr: miniredis.RunT(t), now: time.UnixMilli(1_700_000_000_000)}
	f.mr.SetTime(f.now)
	return f
}

func (f *leaseFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *leaseFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mr.SetTime(f.now)
	f.mu.Unlock()
}

func (f *leaseFixture) strict(limits Limits) *Limiter {
	f.t.Helper()
	client := redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
	f.t.Cleanup(func() { _ = client.Close() })
	l, err := New(client, "rpm", limits)
	if err != nil {
		f.t.Fatal(err)
	}
	return l
}

func (f *leaseFixture) leased(limits Limits, size int, ttl time.Duration) *Leased {
	f.t.Helper()
	l := NewLeased(f.strict(limits), size, ttl)
	l.now = f.clock
	f.t.Cleanup(l.Close)
	return l
}

// frozenLimits refill so slowly that a frozen clock sees no refill at all.
func frozenLimits(burst float64) Limits {
	return Limits{Burst: burst, Rate: 0.001, TTL: time.Minute}
}

func allowN(t *testing.T, l *Leased, tenant string, n int) (admitted int) {
	t.Helper()
	for range n {
		d, err := l.Allow(context.Background(), tenant, 1)
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			admitted++
		}
	}
	return admitted
}

// strictProbe drains what the store still holds through a strict limiter,
// returning the count — the bucket's remaining tokens as the store sees them.
func strictProbe(t *testing.T, l *Limiter, tenant string) (remaining int) {
	t.Helper()
	for range 10_000 {
		d, err := l.Allow(context.Background(), tenant, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Allowed {
			return remaining
		}
		remaining++
	}
	t.Fatal("probe never denied")
	return 0
}

func TestLeaseAcquiredOncePerK(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(50), 10, time.Second)

	if got := allowN(t, l, "team-a", 25); got != 25 {
		t.Fatalf("admitted %d of 25 within capacity", got)
	}
	if st := l.Stats(); st.Acquisitions != 3 || st.LocalAdmits != 22 {
		t.Fatalf("stats = %+v, want 3 acquisitions (leases of 10) and 22 local admits", st)
	}
}

func TestLeaseCumulativeNeverExceedsBucket(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(20), 8, time.Second)

	if got := allowN(t, l, "team-a", 100); got != 20 {
		t.Fatalf("admitted %d, want exactly the capacity 20", got)
	}
	// Leases of 8, 8, 4, then one denial cached for the rest.
	if st := l.Stats(); st.Acquisitions != 4 || st.LocalDenies != 79 {
		t.Fatalf("stats = %+v, want 4 acquisitions and 79 local denials", st)
	}
}

func TestTakeGrantsWhatIsThere(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.strict(frozenLimits(3.7))

	g, err := l.Take(context.Background(), "team-a", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if g.Granted != 3 || g.Remaining < 0.69 || g.Remaining > 0.71 {
		t.Fatalf("grant = %+v, want 3 whole tokens with 0.7 left", g)
	}
	g, err = l.Take(context.Background(), "team-a", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if g.Granted != 0 || g.RetryAfter <= 0 {
		t.Fatalf("second take = %+v, want denial with retry", g)
	}
}

func TestLeaseExpiryRefunds(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(10), 10, time.Second)

	if got := allowN(t, l, "team-a", 1); got != 1 {
		t.Fatal("first request denied")
	}
	// Nine tokens are held locally; the store sees zero.
	if rem := strictProbe(t, f.strict(frozenLimits(10)), "team-a"); rem != 0 {
		t.Fatalf("store remaining = %d while lease is live, want 0", rem)
	}
	f.advance(2 * time.Second)
	l.sweep(context.Background(), false)
	if st := l.Stats(); st.Refunds != 1 || st.RefundFailures != 0 {
		t.Fatalf("stats = %+v, want one refund", st)
	}
	if rem := strictProbe(t, f.strict(frozenLimits(10)), "team-a"); rem != 9 {
		t.Fatalf("store remaining after expiry = %d, want 9", rem)
	}
}

func TestDenialCachedUntilRetry(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(Limits{Burst: 2, Rate: 1, TTL: time.Minute}, 5, time.Second)

	if got := allowN(t, l, "team-a", 2); got != 2 {
		t.Fatalf("admitted %d of 2 within capacity", got)
	}
	// Third request: the store denies (retry 1s); fourth and fifth are
	// answered locally.
	if got := allowN(t, l, "team-a", 3); got != 0 {
		t.Fatalf("admitted %d past capacity", got)
	}
	if st := l.Stats(); st.Acquisitions != 2 || st.LocalDenies != 2 {
		t.Fatalf("stats = %+v, want 2 acquisitions and 2 local denials", st)
	}
	f.advance(time.Second)
	if got := allowN(t, l, "team-a", 1); got != 1 {
		t.Fatal("request after retry interval denied")
	}
	if st := l.Stats(); st.Acquisitions != 3 {
		t.Fatalf("stats = %+v, want a third acquisition after the retry interval", st)
	}
}

func TestCloseReturnsHeldTokens(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(10), 10, time.Minute)

	allowN(t, l, "team-a", 1)
	l.Close()
	if rem := strictProbe(t, f.strict(frozenLimits(10)), "team-a"); rem != 9 {
		t.Fatalf("store remaining after Close = %d, want 9", rem)
	}
}

func TestLeaseRefundFailureIsCounted(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(10), 10, time.Second)
	allowN(t, l, "team-a", 1)
	f.mr.Close()
	f.advance(2 * time.Second)
	l.sweep(context.Background(), false)
	if st := l.Stats(); st.RefundFailures != 1 || st.TokensLost != 9 {
		t.Fatalf("stats = %+v, want one failed refund of 9 tokens", st)
	}
}

// TestLeasedRandomizedBound drives random demand through random replica
// counts and lease sizes and checks the published bound: admitted never
// exceeds the bucket, under-admission is at most M·K, and after every
// replica closes the store holds exactly what was not admitted.
func TestLeasedRandomizedBound(t *testing.T) {
	seed := uint64(time.Now().UnixNano())
	if s, ok := os.LookupEnv("PUSAQ_TEST_SEED"); ok {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		seed = v
	}
	t.Logf("seed=%d (PUSAQ_TEST_SEED reproduces)", seed)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))

	for round := range 20 {
		f := newLeaseFixture(t)
		replicas := 1 + rng.IntN(4)
		size := 1 + rng.IntN(8)
		burst := 5 + rng.IntN(50)
		demand := burst + rng.IntN(3*burst)

		leased := make([]*Leased, replicas)
		for i := range leased {
			leased[i] = f.leased(frozenLimits(float64(burst)), size, time.Minute)
		}

		var admitted atomic.Int64
		var wg sync.WaitGroup
		for range demand {
			l := leased[rng.IntN(replicas)]
			wg.Add(1)
			go func() {
				defer wg.Done()
				d, err := l.Allow(context.Background(), "team-a", 1)
				if err != nil {
					t.Error(err)
					return
				}
				if d.Allowed {
					admitted.Add(1)
				}
			}()
		}
		wg.Wait()

		got := int(admitted.Load())
		want := min(demand, burst)
		if got > want {
			t.Fatalf("round %d (M=%d K=%d B=%d demand=%d): admitted %d > %d", round, replicas, size, burst, demand, got, want)
		}
		if got < want-replicas*size {
			t.Fatalf("round %d (M=%d K=%d B=%d demand=%d): admitted %d < %d − M·K", round, replicas, size, burst, demand, got, want)
		}
		for _, l := range leased {
			l.Close()
		}
		remaining := strictProbe(t, f.strict(frozenLimits(float64(burst))), "team-a")
		if got+remaining != burst {
			t.Fatalf("round %d (M=%d K=%d B=%d demand=%d): admitted %d + remaining %d != B", round, replicas, size, burst, demand, got, remaining)
		}
		f.mr.Close()
	}
}
