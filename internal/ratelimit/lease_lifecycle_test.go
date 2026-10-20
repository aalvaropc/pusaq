package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestLeaseAcquisitionRejectsRetiredEntry(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(10), 3, time.Hour)
	stale := l.leaseFor("team-a")
	l.retireLease("team-a", stale)
	current := l.lockLease("team-a", stale)
	defer current.mu.Unlock()
	l.mu.Lock()
	registered := l.leases["team-a"]
	l.mu.Unlock()
	if current == stale || current != registered {
		t.Fatal("admission acquired a retired entry instead of the current registered entry")
	}
}

type gatedLeaseStore struct {
	redis.Scripter
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *gatedLeaseStore) EvalSha(ctx context.Context, sha string, keys []string, args ...any) *redis.Cmd {
	if len(keys) == 1 && keys[0] == "rl:{team-a}:rpm" {
		s.once.Do(func() {
			close(s.entered)
			select {
			case <-s.release:
			case <-ctx.Done():
			}
		})
	}
	return s.Scripter.EvalSha(ctx, sha, keys, args...)
}

func TestLeaseRetirementIsolatesBusyTenant(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(10), 3, time.Hour)
	store := &gatedLeaseStore{Scripter: l.inner.client, entered: make(chan struct{}), release: make(chan struct{})}
	l.inner.client = store
	var release sync.Once
	unblock := func() { release.Do(func() { close(store.release) }) }
	t.Cleanup(unblock)
	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		if d, err := l.Allow(context.Background(), "team-a", 1); err != nil || !d.Allowed {
			t.Errorf("tenant a admission = %+v, %v", d, err)
		}
	}()
	<-store.entered
	entry := l.leaseFor("team-a")
	retired := make(chan struct{})
	go func() {
		l.retireLease("team-a", entry)
		close(retired)
	}()
	defer func() {
		unblock()
		<-aDone
		<-retired
	}()
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("retirement waited for a busy tenant")
	}
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		if d, err := l.Allow(context.Background(), "team-b", 1); err != nil || !d.Allowed {
			t.Errorf("tenant b admission = %+v, %v", d, err)
		}
	}()
	select {
	case <-bDone:
	case <-time.After(time.Second):
		unblock()
		<-bDone
		t.Fatal("tenant b waited for tenant a")
	}
	select {
	case <-aDone:
		t.Fatal("tenant a completed before its store operation was released")
	default:
	}
}

func TestLeaseRetirementPreservesLiveState(t *testing.T) {
	for _, state := range []string{"tokens", "pending", "denial", "replacement"} {
		t.Run(state, func(t *testing.T) {
			f := newLeaseFixture(t)
			l := f.leased(frozenLimits(10), 3, time.Hour)
			entry := l.leaseFor("team-a")
			entry.mu.Lock()
			switch state {
			case "tokens":
				entry.tokens = 2
			case "pending":
				entry.pending = 2
			case "denial":
				entry.denyUntil = f.clock().Add(time.Second)
			}
			entry.mu.Unlock()
			want := entry
			if state == "replacement" {
				want = &lease{}
				l.mu.Lock()
				l.leases["team-a"] = want
				l.mu.Unlock()
			}
			l.retireLease("team-a", entry)
			l.mu.Lock()
			got := l.leases["team-a"]
			l.mu.Unlock()
			if got != want {
				t.Fatalf("retirement discarded %s state", state)
			}
		})
	}
}

func TestLeaseCleanupConservesTokensUnderConcurrency(t *testing.T) {
	f := newLeaseFixture(t)
	l := f.leased(frozenLimits(1000), 3, time.Hour)
	var admitted atomic.Int64
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 4 {
		workers.Go(func() {
			<-start
			for range 250 {
				d, err := l.Allow(context.Background(), "team-a", 1)
				if err != nil {
					t.Error(err)
					return
				}
				if d.Allowed {
					admitted.Add(1)
				}
			}
		})
	}
	workers.Go(func() {
		<-start
		for range 1000 {
			l.sweep(context.Background(), false)
		}
	})
	close(start)
	workers.Wait()
	l.Close()
	remaining, err := strconv.ParseFloat(f.mr.HGet("rl:{team-a}:rpm", "tokens"), 64)
	stats := l.Stats()
	if err != nil || remaining+float64(admitted.Load()) != 1000 || stats.TokensLost != 0 || stats.RefundFailures != 0 {
		t.Fatalf("remaining = %v admitted = %d stats = %+v error = %v", remaining, admitted.Load(), stats, err)
	}
	t.Logf("1000 requests and 1000 sweeps: admitted %d + remaining %.0f = 1000; lost 0", admitted.Load(), remaining)
}

func TestLeaseDenialCacheUsesSmallerRetryAndTTL(t *testing.T) {
	for _, retry := range []time.Duration{500 * time.Millisecond, time.Second, 10 * time.Second} {
		t.Run(retry.String(), func(t *testing.T) {
			f := newLeaseFixture(t)
			l := f.leased(Limits{Burst: 1, Rate: float64(time.Second) / float64(retry), TTL: time.Hour}, 1, time.Second)
			if got := allowN(t, l, "team-a", 1); got != 1 {
				t.Fatalf("initial admission = %d", got)
			}
			if d, err := l.Allow(context.Background(), "team-a", 1); err != nil || d.Allowed || d.RetryAfter != retry {
				t.Fatalf("initial denial = %+v, %v", d, err)
			}
			if err := l.inner.Adjust(context.Background(), "team-a", 1); err != nil {
				t.Fatal(err)
			}
			interval := min(retry, time.Second)
			f.advance(interval - time.Millisecond)
			if d, err := l.Allow(context.Background(), "team-a", 1); err != nil || d.Allowed || d.RetryAfter != time.Millisecond {
				t.Fatalf("cached denial = %+v, %v", d, err)
			}
			if stats := l.Stats(); stats.Acquisitions != 2 || stats.LocalDenies != 1 {
				t.Fatalf("stats before expiry = %+v", stats)
			}
			f.advance(time.Millisecond)
			if got := allowN(t, l, "team-a", 1); got != 1 {
				t.Fatal("returned token unavailable after denial cache expired")
			}
			if stats := l.Stats(); stats.Acquisitions != 3 {
				t.Fatalf("acquisitions after expiry = %d", stats.Acquisitions)
			}
		})
	}
}
