package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func storeClockLimiter(t *testing.T) (*Limiter, *miniredis.Miniredis, time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	now := time.UnixMilli(1_700_000_000_000)
	mr.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	l, err := New(client, "rpm", limits())
	if err != nil {
		t.Fatal(err)
	}
	return l, mr, now
}

func TestRefillFollowsStoreTime(t *testing.T) {
	l, mr, now := storeClockLimiter(t)
	ctx := context.Background()
	if d, err := l.Allow(ctx, "team-a", 5); err != nil || !d.Allowed {
		t.Fatalf("drain = %+v, %v", d, err)
	}
	if got := mr.HGet("rl:{team-a}:rpm", "ts"); got != strconv.FormatInt(now.UnixMilli(), 10) {
		t.Fatalf("bucket timestamp = %s, want store time %d", got, now.UnixMilli())
	}
	mr.SetTime(now.Add(time.Second))
	if d, err := l.Allow(ctx, "team-a", 1); err != nil || !d.Allowed {
		t.Fatalf("one second refill = %+v, %v", d, err)
	}
	if d, err := l.Allow(ctx, "team-a", 1); err != nil || d.Allowed {
		t.Fatalf("extra refill = %+v, %v", d, err)
	}
}

func TestBucketOperationsPreserveTimestampOnClockRollback(t *testing.T) {
	for _, operation := range []string{"allow", "adjust", "take"} {
		t.Run(operation, func(t *testing.T) {
			l, mr, now := storeClockLimiter(t)
			ctx := context.Background()
			if d, err := l.Allow(ctx, "team-a", 5); err != nil || !d.Allowed {
				t.Fatalf("drain = %+v, %v", d, err)
			}
			for range 3 {
				mr.SetTime(now.Add(-time.Second))
				switch operation {
				case "allow":
					if d, err := l.Allow(ctx, "team-a", 1); err != nil || d.Allowed {
						t.Fatalf("rollback admission = %+v, %v", d, err)
					} else if d.RetryAfter != 2*time.Second {
						t.Fatalf("rollback retry = %v, want 2s", d.RetryAfter)
					}
				case "adjust":
					if err := l.Adjust(ctx, "team-a", 0); err != nil {
						t.Fatal(err)
					}
				case "take":
					if d, err := l.Take(ctx, "team-a", 1, 3); err != nil || d.Granted != 0 {
						t.Fatalf("rollback lease = %+v, %v", d, err)
					} else if d.RetryAfter != 2*time.Second {
						t.Fatalf("rollback lease retry = %v, want 2s", d.RetryAfter)
					}
				}
				if got := mr.HGet("rl:{team-a}:rpm", "ts"); got != strconv.FormatInt(now.UnixMilli(), 10) {
					t.Fatalf("timestamp = %s, want unchanged %d", got, now.UnixMilli())
				}
				mr.SetTime(now)
				if d, err := l.Allow(ctx, "team-a", 1); err != nil || d.Allowed {
					t.Fatalf("catch-up credited extra refill = %+v, %v", d, err)
				}
			}
			mr.SetTime(now.Add(time.Second))
			if d, err := l.Allow(ctx, "team-a", 1); err != nil || !d.Allowed {
				t.Fatalf("refill after rollback = %+v, %v", d, err)
			}
		})
	}
}

func TestExistingBucketAheadOfStoreTimeKeepsItsBalance(t *testing.T) {
	l, mr, now := storeClockLimiter(t)
	ahead := now.Add(time.Minute)
	mr.HSet("rl:{team-a}:rpm", "tokens", "0", "ts", strconv.FormatInt(ahead.UnixMilli(), 10))
	ctx := context.Background()
	if d, err := l.Allow(ctx, "team-a", 1); err != nil || d.Allowed || d.Remaining != 0 {
		t.Fatalf("existing bucket = %+v, %v", d, err)
	} else if d.RetryAfter != time.Minute+time.Second {
		t.Fatalf("existing bucket retry = %v, want 61s", d.RetryAfter)
	}
	if got := mr.HGet("rl:{team-a}:rpm", "ts"); got != strconv.FormatInt(ahead.UnixMilli(), 10) {
		t.Fatalf("existing timestamp changed: %s", got)
	}
	mr.SetTime(ahead)
	if d, err := l.Allow(ctx, "team-a", 1); err != nil || d.Allowed {
		t.Fatalf("refill before catch-up = %+v, %v", d, err)
	}
	mr.SetTime(ahead.Add(time.Second))
	if d, err := l.Allow(ctx, "team-a", 1); err != nil || !d.Allowed {
		t.Fatalf("refill after catch-up = %+v, %v", d, err)
	}
}

func TestStoreClockSharedAcrossLeasedReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	now := time.UnixMilli(1_700_000_000_000)
	mr.SetTime(now)
	replicas := make([]*Leased, 2)
	for i := range replicas {
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		inner, err := New(client, "rpm", Limits{Burst: 10, Rate: 1, TTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		l := NewLeased(inner, 3, time.Hour)
		localTime := now.Add(time.Duration(i*2-1) * 24 * time.Hour)
		l.now = func() time.Time { return localTime }
		t.Cleanup(l.Close)
		replicas[i] = l
	}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := range 200 {
		l := replicas[i%len(replicas)]
		wg.Go(func() {
			d, err := l.Allow(context.Background(), "team-a", 1)
			if err != nil {
				t.Error(err)
				return
			}
			if d.Allowed {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if got := admitted.Load(); got != 10 {
		t.Fatalf("admitted %d of 200, want 10", got)
	}
	if got := mr.HGet("rl:{team-a}:rpm", "ts"); got != strconv.FormatInt(now.UnixMilli(), 10) {
		t.Fatalf("bucket timestamp = %s, want shared store time", got)
	}
}
