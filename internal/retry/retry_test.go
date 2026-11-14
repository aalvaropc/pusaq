package retry

import (
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newBudget(ratio float64) (*Budget, *clock) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	b := NewBudget(ratio)
	b.SetClock(c.now)
	return b, c
}

func drain(b *Budget) int {
	n := 0
	for b.Withdraw() {
		n++
	}
	return n
}

func TestBudgetStartsWithTheReserve(t *testing.T) {
	b, _ := newBudget(0.5)
	if got := drain(b); got != Reserve {
		t.Fatalf("withdrawn = %d, want the reserve %v", got, Reserve)
	}
}

func TestDepositsCreditTheRatio(t *testing.T) {
	b, _ := newBudget(0.25)
	drain(b)
	for range 3 {
		b.Deposit()
	}
	if b.Withdraw() {
		t.Fatal("0.75 of a retry paid a retry")
	}
	b.Deposit()
	if !b.Withdraw() || b.Withdraw() {
		t.Fatal("four credits of 0.25 must pay exactly one retry")
	}
}

func TestDepositsNeverExceedTheReserve(t *testing.T) {
	b, _ := newBudget(1)
	for range 1000 {
		b.Deposit()
	}
	if got := drain(b); got != Reserve {
		t.Fatalf("withdrawn = %d after 1000 credits, want the reserve %v", got, Reserve)
	}
}

func TestFloorRefillsOnePerSecondUpToTheReserve(t *testing.T) {
	b, c := newBudget(0.25)
	drain(b)
	c.t = c.t.Add(999 * time.Millisecond)
	if b.Withdraw() {
		t.Fatal("a retry before a full second of refill")
	}
	c.t = c.t.Add(time.Millisecond)
	if !b.Withdraw() || b.Withdraw() {
		t.Fatal("one second must refill exactly one retry")
	}
	c.t = c.t.Add(time.Hour)
	if got := drain(b); got != Reserve {
		t.Fatalf("withdrawn after an idle hour = %d, want the reserve %v", got, Reserve)
	}
}

func TestClockGoingBackRefillsNothing(t *testing.T) {
	b, c := newBudget(0.25)
	drain(b)
	c.t = c.t.Add(-time.Hour)
	if b.Withdraw() {
		t.Fatal("a backwards clock refilled the budget")
	}
	c.t = c.t.Add(time.Hour + 999*time.Millisecond)
	if b.Withdraw() {
		t.Fatal("the backwards hour was counted as refill time")
	}
	c.t = c.t.Add(time.Millisecond)
	if !b.Withdraw() {
		t.Fatal("one second past the last counted instant refilled nothing")
	}
}

func TestRefundReturnsAWithdrawal(t *testing.T) {
	b, _ := newBudget(0.25)
	drain(b)
	b.Refund()
	if !b.Withdraw() || b.Withdraw() {
		t.Fatal("a refund must pay exactly one retry")
	}
	for range int(2 * Reserve) {
		b.Refund()
	}
	if got := drain(b); got != Reserve {
		t.Fatalf("withdrawn = %d after refunds, want the reserve %v", got, Reserve)
	}
}

func TestBudgetBoundOverARun(t *testing.T) {
	for _, tc := range []struct {
		ratio    float64
		requests int
		seconds  int
	}{
		{0.2, 1000, 0},
		{0.2, 1000, 30},
		{0.1, 5000, 60},
		{1, 100, 0},
	} {
		b, c := newBudget(tc.ratio)
		retries := 0
		step := time.Duration(tc.seconds) * time.Second / time.Duration(tc.requests)
		for range tc.requests {
			c.t = c.t.Add(step)
			b.Deposit()
			for range 3 {
				if b.Withdraw() {
					retries++
				}
			}
		}
		bound := Reserve + float64(tc.seconds)*RefillPerSecond + tc.ratio*float64(tc.requests)
		if float64(retries) > bound || float64(retries) < bound-2 {
			t.Fatalf("ratio %v, %d requests over %ds: %d retries, bound %.1f", tc.ratio, tc.requests, tc.seconds, retries, bound)
		}
		t.Logf("ratio %v, %d requests over %ds wanting 3 retries each: %d retries (bound %.1f)", tc.ratio, tc.requests, tc.seconds, retries, bound)
	}
}

func TestBudgetIsSafeForConcurrentUse(t *testing.T) {
	b, _ := newBudget(0.5)
	var mu sync.Mutex
	retries := 0
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 100 {
				b.Deposit()
				if b.Withdraw() {
					mu.Lock()
					retries++
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	if bound := int(Reserve) + 50*100/2; retries > bound || retries < bound-50 {
		t.Fatalf("retries = %d, want at most %d and at least %d", retries, bound, bound-50)
	}
}

func TestBackoffCeilingDoublesToTheCap(t *testing.T) {
	want := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond, 2 * time.Second, 2 * time.Second}
	for i, w := range want {
		n := i + 1
		if got := Backoff(n, 0.999999999); got > w || got < w-time.Millisecond {
			t.Fatalf("Backoff(%d, ~1) = %v, want just under %v", n, got, w)
		}
		if got := Backoff(n, 0.5); got != w/2 {
			t.Fatalf("Backoff(%d, 0.5) = %v, want %v", n, got, w/2)
		}
		if got := Backoff(n, 0); got != 0 {
			t.Fatalf("Backoff(%d, 0) = %v, want 0 (full jitter)", n, got)
		}
	}
	for _, n := range []int{0, -1, 17, 64, 1 << 20} {
		if got := Backoff(n, 0.5); got != BackoffCap/2 {
			t.Fatalf("Backoff(%d, 0.5) = %v, want half the cap", n, got)
		}
	}
}
