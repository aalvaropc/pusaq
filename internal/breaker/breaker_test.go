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

func failN(b *Breaker, n int) {
	for range n {
		if b.Allow() {
			b.Record(false)
		}
	}
}

func TestOpensAtThresholdWithMinSamples(t *testing.T) {
	b, _ := testBreaker()
	failN(b, 4) // 4/4 failures ≥ min samples, rate 1.0 > 0.5
	if got := b.State(); got != StateOpen {
		t.Fatalf("state = %s, want open", got)
	}
	if b.Allow() {
		t.Fatal("open breaker allowed an attempt")
	}
}

func TestNeverOpensBelowMinSamples(t *testing.T) {
	b, _ := testBreaker()
	failN(b, 3) // all failures but below MinSamples=4
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s, want closed below min samples", got)
	}
	if !b.Allow() {
		t.Fatal("closed breaker denied an attempt")
	}
}

func TestMixedTrafficBelowThresholdStaysClosed(t *testing.T) {
	b, _ := testBreaker()
	for i := range 10 {
		if b.Allow() {
			b.Record(i%2 == 0) // 50% failures — not > 0.5
		}
	}
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s, want closed at exactly the threshold", got)
	}
}

func TestCooldownGrantsBoundedProbes(t *testing.T) {
	b, now := testBreaker()
	failN(b, 4)
	if b.Allow() {
		t.Fatal("allowed while open before cooldown")
	}

	*now = now.Add(10 * time.Second)
	if got := b.State(); got != StateHalfOpen {
		t.Fatalf("state = %s, want half_open after cooldown", got)
	}
	// Exactly Probes=2 concurrent probes, no more.
	for i := range 2 {
		if !b.Allow() {
			t.Fatalf("half-open denied probe %d", i+1)
		}
	}
	if b.Allow() {
		t.Fatal("half-open exceeded its probe budget")
	}
}

func TestProbeSuccessClosesAndFailureReopens(t *testing.T) {
	b, now := testBreaker()
	failN(b, 4)
	*now = now.Add(10 * time.Second)

	if !b.Allow() {
		t.Fatal("probe denied")
	}
	b.Record(true)
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s after probe success, want closed", got)
	}
	// A fresh window: old failures don't linger.
	if !b.Allow() {
		t.Fatal("closed-after-recovery denied traffic")
	}
	b.Record(false)
	if got := b.State(); got != StateClosed {
		t.Fatal("one failure after recovery reopened the breaker")
	}

	// Now reopen via a failed probe.
	failN(b, 4)
	*now = now.Add(10 * time.Second)
	if !b.Allow() {
		t.Fatal("probe denied")
	}
	b.Record(false)
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
		if b.Allow() {
			b.Record(false)
		}
	}
	for range 10 {
		if b.Allow() {
			b.Record(true)
		}
	}
	for range 5 {
		if b.Allow() {
			b.Record(false)
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
			if b.Allow() {
				b.Record(!fail)
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
	if !b.Allow() { // open → half_open, probe granted
		t.Fatal("probe not granted after cooldown")
	}
	b.Record(false) // half_open → open
	now = now.Add(time.Second)
	if !b.Allow() {
		t.Fatal("second probe not granted")
	}
	b.Record(true) // half_open → closed

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
