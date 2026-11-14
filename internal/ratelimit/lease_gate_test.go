package ratelimit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/storegate"
)

type countingFault struct {
	calls atomic.Int64
	fail  atomic.Bool
}

func (f *countingFault) DialHook(next redis.DialHook) redis.DialHook { return next }

func (f *countingFault) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		f.calls.Add(1)
		if f.fail.Load() {
			err := errors.New("dial tcp: connect: connection refused")
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

func (f *countingFault) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestLeaseModeUnderAnOpenStoreGate(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	gate := storegate.Attach(client, nil)
	fault := &countingFault{}
	client.AddHook(fault)
	inner, err := New(client, "rpm", Limits{Burst: 100, Rate: 1, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	l := NewLeased(inner, 5, time.Hour)
	ctx := context.Background()
	for _, tenant := range []string{"holder", "refunded"} {
		if d, err := l.Allow(ctx, tenant, 1); err != nil || !d.Allowed {
			t.Fatalf("%s lease: %+v, %v", tenant, d, err)
		}
	}

	fault.fail.Store(true)
	for range storegate.MinSamples {
		_, _ = inner.Allow(ctx, "other", 1)
	}
	if got := gate.State(); got != breaker.StateOpen {
		t.Fatalf("gate state = %s, want open", got)
	}
	before := fault.calls.Load()
	for i := range 4 {
		if d, err := l.Allow(ctx, "holder", 1); err != nil || !d.Allowed {
			t.Fatalf("held token %d: %+v, %v", i, d, err)
		}
	}
	if _, err := l.Allow(ctx, "holder", 1); !errors.Is(err, storegate.ErrOpen) {
		t.Fatalf("admission past the lease: error = %v, want ErrOpen", err)
	}
	l.Close()
	if got := fault.calls.Load(); got != before {
		t.Fatalf("store calls went from %d to %d with the gate open", before, got)
	}
	st := l.Stats()
	if st.LocalAdmits != 4 || st.RefundFailures != 1 || st.TokensLost != 4 || st.Refunds != 0 {
		t.Fatalf("stats = %+v, want 4 local admits and the held 4 tokens lost in one skipped refund", st)
	}
}
