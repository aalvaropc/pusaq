package breaker

import (
	"sync"
	"testing"
	"time"
)

func testBreaker() (*Breaker, *time.Time) {
	b := New(Config{Window: 10, MinSamples: 4, FailureThreshold: 0.5, Cooldown: 10 * time.Second, Probes: 2})
	now := time.UnixMilli(1_700_000_000_000)
	b.SetClock(func() time.Time { return now })
	return b, &now
}

func allowed(b *Breaker) bool {
	_, ok := b.Allow()
	return ok
}

func mustAllow(t *testing.T, b *Breaker, attempt string) Ticket {
	t.Helper()
	tk, ok := b.Allow()
	if !ok {
		t.Fatalf("breaker denied the %s", attempt)
	}
	return tk
}

func failN(b *Breaker, n int) {
	for range n {
		if tk, ok := b.Allow(); ok {
			tk.Record(false)
		}
	}
}

func TestOpensAtThresholdWithMinSamples(t *testing.T) {
	b, _ := testBreaker()
	failN(b, 4) // 4/4 failures ≥ min samples, rate 1.0 > 0.5
	if got := b.State(); got != StateOpen {
		t.Fatalf("state = %s, want open", got)
	}
	if allowed(b) {
		t.Fatal("open breaker allowed an attempt")
	}
}

func TestNeverOpensBelowMinSamples(t *testing.T) {
	b, _ := testBreaker()
	failN(b, 3) // all failures but below MinSamples=4
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s, want closed below min samples", got)
	}
	if !allowed(b) {
		t.Fatal("closed breaker denied an attempt")
	}
}

func TestMixedTrafficBelowThresholdStaysClosed(t *testing.T) {
	b, _ := testBreaker()
	for i := range 10 {
		if tk, ok := b.Allow(); ok {
			tk.Record(i%2 == 0) // 50% failures — not > 0.5
		}
	}
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s, want closed at exactly the threshold", got)
	}
}

func TestCooldownGrantsBoundedProbes(t *testing.T) {
	b, now := testBreaker()
	failN(b, 4)
	if allowed(b) {
		t.Fatal("allowed while open before cooldown")
	}

	*now = now.Add(10 * time.Second)
	if got := b.State(); got != StateHalfOpen {
		t.Fatalf("state = %s, want half_open after cooldown", got)
	}
	// Exactly Probes=2 concurrent probes, no more.
	for i := range 2 {
		if !allowed(b) {
			t.Fatalf("half-open denied probe %d", i+1)
		}
	}
	if allowed(b) {
		t.Fatal("half-open exceeded its probe budget")
	}
}

func TestReleaseReturnsProbeSlot(t *testing.T) {
	b, now := testBreaker()
	failN(b, 4)
	*now = now.Add(10 * time.Second)
	var probe Ticket
	for range 2 {
		probe = mustAllow(t, b, "probe")
	}
	if allowed(b) {
		t.Fatal("half-open exceeded its probe budget")
	}

	probe.Release()
	if !allowed(b) {
		t.Fatal("released probe slot was not reusable")
	}
	if got := b.State(); got != StateHalfOpen {
		t.Fatalf("state = %s, want half_open: a release records no outcome", got)
	}
}

func TestReleaseIsNoOpWhenClosed(t *testing.T) {
	b, _ := testBreaker()
	failN(b, 3)
	mustAllow(t, b, "closed attempt").Release()
	failN(b, 1)
	if got := b.State(); got != StateOpen {
		t.Fatalf("state = %s, want open: a release must not erase failures", got)
	}
}

func TestProbeSuccessClosesAndFailureReopens(t *testing.T) {
	b, now := testBreaker()
	failN(b, 4)
	*now = now.Add(10 * time.Second)

	mustAllow(t, b, "probe").Record(true)
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s after probe success, want closed", got)
	}
	// A fresh window: old failures don't linger.
	mustAllow(t, b, "closed-after-recovery attempt").Record(false)
	if got := b.State(); got != StateClosed {
		t.Fatal("one failure after recovery reopened the breaker")
	}

	// Now reopen via a failed probe.
	failN(b, 4)
	*now = now.Add(10 * time.Second)
	mustAllow(t, b, "probe").Record(false)
	if got := b.State(); got != StateOpen {
		t.Fatalf("state = %s after probe failure, want open", got)
	}
}

func TestSlidingWindowEvictsOldOutcomes(t *testing.T) {
	// Successes push old failures out of the window. 2 early failures
	// (never above threshold), 10 successes (evicting them), then 5 fresh
	// failures: 5/10 = 0.5 is NOT above the threshold — but if the 2
	// evicted failures still counted (7/10 = 0.7), the breaker would
	// wrongly open.
	b, _ := testBreaker()
	for range 2 {
		if tk, ok := b.Allow(); ok {
			tk.Record(false)
		}
	}
	for range 10 {
		if tk, ok := b.Allow(); ok {
			tk.Record(true)
		}
	}
	for range 5 {
		if tk, ok := b.Allow(); ok {
			tk.Record(false)
		}
	}
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s, want closed (old failures evicted)", got)
	}
}

func TestConcurrentAllowRecordIsSafe(t *testing.T) {
	b := New(Config{})
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(fail bool) {
			defer wg.Done()
			if tk, ok := b.Allow(); ok {
				tk.Record(!fail)
			}
			_ = b.State()
		}(i%3 == 0)
	}
	wg.Wait()
}

func TestOnStateChangeReportsEveryTransition(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	var seen []string
	b := New(Config{Window: 4, MinSamples: 2, FailureThreshold: 0.5, Cooldown: time.Second, Probes: 1,
		OnStateChange: func(from, to State) { seen = append(seen, string(from)+">"+string(to)) }})
	b.SetClock(func() time.Time { return now })

	failN(b, 3) // closed → open
	now = now.Add(time.Second)
	mustAllow(t, b, "probe after cooldown").Record(false) // open → half_open → open
	now = now.Add(time.Second)
	mustAllow(t, b, "second probe").Record(true) // half_open → closed

	want := []string{"closed>open", "open>half_open", "half_open>open", "open>half_open", "half_open>closed"}
	if len(seen) != len(want) {
		t.Fatalf("transitions = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", seen, want)
		}
	}
}

func TestOnStateChangeRunsOutsideTheLock(t *testing.T) {
	var b *Breaker
	var observed State
	b = New(Config{Window: 4, MinSamples: 2, FailureThreshold: 0.5,
		OnStateChange: func(_, _ State) { observed = b.State() }}) // would deadlock under the lock
	failN(b, 3)
	if observed != StateOpen {
		t.Fatalf("hook observed %q, want open", observed)
	}
}

var staleOrigins = []string{"closed", "previous half-open"}

func halfOpenWithStale(t *testing.T, origin string) (b *Breaker, stale, probe Ticket) {
	t.Helper()
	b, now := testBreaker()
	if origin == "closed" {
		stale = mustAllow(t, b, "closed attempt")
		failN(b, 4)
	} else {
		failN(b, 4)
		*now = now.Add(10 * time.Second)
		stale = mustAllow(t, b, "previous probe")
		mustAllow(t, b, "previous probe").Record(false)
	}
	*now = now.Add(10 * time.Second)
	probe = mustAllow(t, b, "probe")
	mustAllow(t, b, "probe")
	return b, stale, probe
}

func TestStaleOutcomeDoesNotDecideHalfOpen(t *testing.T) {
	tests := []struct {
		name         string
		staleSuccess bool
		probeSuccess bool
		want         State
	}{
		{"stale success, probe success", true, true, StateClosed},
		{"stale success, probe failure", true, false, StateOpen},
		{"stale failure, probe success", false, true, StateClosed},
		{"stale failure, probe failure", false, false, StateOpen},
	}
	for _, origin := range staleOrigins {
		for _, tt := range tests {
			t.Run(origin+"/"+tt.name, func(t *testing.T) {
				b, stale, probe := halfOpenWithStale(t, origin)

				stale.Record(tt.staleSuccess)
				if got := b.State(); got != StateHalfOpen {
					t.Fatalf("state = %s after a stale outcome, want half_open", got)
				}
				if allowed(b) {
					t.Fatal("a stale outcome freed a probe slot")
				}
				probe.Record(tt.probeSuccess)
				if got := b.State(); got != tt.want {
					t.Fatalf("state = %s after the probe, want %s", got, tt.want)
				}
			})
		}
	}
}

func TestStaleReleaseFreesNoProbeSlot(t *testing.T) {
	for _, origin := range staleOrigins {
		t.Run(origin, func(t *testing.T) {
			b, stale, probe := halfOpenWithStale(t, origin)

			stale.Release()
			if allowed(b) {
				t.Fatal("a stale release freed a probe slot")
			}
			probe.Release()
			if !allowed(b) {
				t.Fatal("a current probe's release did not free its slot")
			}
		})
	}
}

func TestStaleOutcomeStaysOutOfRecoveredWindow(t *testing.T) {
	for _, origin := range []string{"closed", "sibling probe"} {
		t.Run(origin, func(t *testing.T) {
			b, now := testBreaker()
			var stale Ticket
			if origin == "closed" {
				stale = mustAllow(t, b, "closed attempt")
			}
			failN(b, 4)
			*now = now.Add(10 * time.Second)
			probe := mustAllow(t, b, "probe")
			if origin == "sibling probe" {
				stale = mustAllow(t, b, "sibling probe")
			}
			probe.Record(true)

			stale.Record(false)
			failN(b, 3)
			if got := b.State(); got != StateClosed {
				t.Fatalf("state = %s, want closed: the recovered window holds 3 failures, below the minimum of 4", got)
			}
			failN(b, 1)
			if got := b.State(); got != StateOpen {
				t.Fatalf("state = %s, want open on 4 failures admitted after recovery", got)
			}
		})
	}
}

func TestZeroTicketIsNoOp(t *testing.T) {
	var tk Ticket
	tk.Record(false)
	tk.Release()
}
