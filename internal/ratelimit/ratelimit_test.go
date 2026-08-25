package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestLimiter runs the real Lua path against miniredis with a frozen
// clock the test controls.
func newTestLimiter(t *testing.T, limits Limits) (*Limiter, *miniredis.Miniredis, *time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	l, err := New(client, "rpm", limits)
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_700_000_000_000)
	l.now = func() time.Time { return now }
	return l, mr, &now
}

func limits() Limits {
	return Limits{Burst: 5, Rate: 1, TTL: time.Minute} // 5 burst, 1 token/s
}

func TestBucketStartsFullThenDenies(t *testing.T) {
	l, _, _ := newTestLimiter(t, limits())
	ctx := context.Background()

	for i := range 5 {
		d, err := l.Allow(ctx, "team-a", 1)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Allowed {
			t.Fatalf("request %d denied within burst", i+1)
		}
	}
	d, err := l.Allow(ctx, "team-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatal("request beyond burst was admitted")
	}
	if d.RetryAfter <= 0 || d.RetryAfter > 2*time.Second {
		t.Fatalf("RetryAfter = %s, want ~1s for rate 1/s", d.RetryAfter)
	}
}

func TestRefillRestoresAndNeverExceedsCapacity(t *testing.T) {
	l, _, now := newTestLimiter(t, limits())
	ctx := context.Background()

	// Drain the bucket.
	for range 5 {
		_, _ = l.Allow(ctx, "team-a", 1)
	}
	if d, _ := l.Allow(ctx, "team-a", 1); d.Allowed {
		t.Fatal("bucket not drained")
	}

	// One second accrues exactly one token.
	*now = now.Add(time.Second)
	if d, _ := l.Allow(ctx, "team-a", 1); !d.Allowed {
		t.Fatal("token not restored after refill interval")
	}
	if d, _ := l.Allow(ctx, "team-a", 1); d.Allowed {
		t.Fatal("second token admitted after only one second of refill")
	}

	// A long idle period caps at capacity, not beyond.
	*now = now.Add(time.Hour)
	admitted := 0
	for range 10 {
		if d, _ := l.Allow(ctx, "team-a", 1); d.Allowed {
			admitted++
		}
	}
	if admitted != 5 {
		t.Fatalf("admitted %d after long idle, want exactly capacity 5", admitted)
	}
}

func TestTenantsAreIsolated(t *testing.T) {
	l, _, _ := newTestLimiter(t, limits())
	ctx := context.Background()

	for range 5 {
		_, _ = l.Allow(ctx, "noisy", 1)
	}
	if d, _ := l.Allow(ctx, "noisy", 1); d.Allowed {
		t.Fatal("noisy tenant not limited")
	}
	if d, _ := l.Allow(ctx, "quiet", 1); !d.Allowed {
		t.Fatal("quiet tenant was punished for the noisy one")
	}
}

func TestIdleBucketGetsTTL(t *testing.T) {
	l, mr, _ := newTestLimiter(t, limits())
	if _, err := l.Allow(context.Background(), "team-a", 1); err != nil {
		t.Fatal(err)
	}
	ttl := mr.TTL("rl:{team-a}:rpm")
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("bucket TTL = %s, want (0, 1m]", ttl)
	}
}

// TestExactnessAcrossReplicasUnderConcurrency is the project's money test:
// two Limiter instances (two "replicas") share one store; with a frozen
// clock no refill accrues, so across any interleaving of concurrent
// requests exactly the bucket capacity may pass.
func TestExactnessAcrossReplicasUnderConcurrency(t *testing.T) {
	mr := miniredis.RunT(t)
	frozen := time.UnixMilli(1_700_000_000_000)
	newReplica := func() *Limiter {
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		l, err := New(client, "rpm", Limits{Burst: 10, Rate: 0.001, TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		l.now = func() time.Time { return frozen }
		return l
	}
	replicaA, replicaB := newReplica(), newReplica()

	const attempts = 200
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := range attempts {
		replica := replicaA
		if i%2 == 1 {
			replica = replicaB
		}
		wg.Add(1)
		go func(l *Limiter) {
			defer wg.Done()
			d, err := l.Allow(context.Background(), "team-a", 1)
			if err != nil {
				t.Error(err)
				return
			}
			if d.Allowed {
				admitted.Add(1)
			}
		}(replica)
	}
	wg.Wait()

	if got := admitted.Load(); got != 10 {
		t.Fatalf("admitted %d of %d concurrent requests, want exactly the capacity 10", got, attempts)
	}
}

func TestStoreFailureSurfacesAsError(t *testing.T) {
	l, mr, _ := newTestLimiter(t, limits())
	mr.Close()
	if _, err := l.Allow(context.Background(), "team-a", 1); err == nil {
		t.Fatal("want error when the store is down (caller fails open)")
	}
}

func TestLimitsValidate(t *testing.T) {
	for _, bad := range []Limits{
		{Burst: 0, Rate: 1, TTL: time.Minute},
		{Burst: 1, Rate: 0, TTL: time.Minute},
		{Burst: 1, Rate: 1, TTL: 0},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("Validate(%+v) accepted invalid limits", bad)
		}
	}
}
