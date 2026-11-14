// Package retry bounds same-backend retries: an instance-wide budget that
// keeps a failing upstream from multiplying its load, and the jittered
// backoff between attempts. It is pure state with an injectable clock.
package retry

import (
	"sync"
	"time"
)

// Budget reserve, floor and backoff constants (docs/design.md §5.6).
const (
	Reserve         = 10.0
	RefillPerSecond = 1.0
	BackoffBase     = 200 * time.Millisecond
	BackoffCap      = 2 * time.Second
)

// Budget is a token bucket of retries shared by every request of one
// gateway instance: each request's first upstream attempt credits it with
// ratio, it refills at RefillPerSecond, holds at most Reserve, and each
// retry takes one. Safe for concurrent use.
type Budget struct {
	mu     sync.Mutex
	ratio  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewBudget returns a full budget crediting ratio of a retry per request.
func NewBudget(ratio float64) *Budget {
	b := &Budget{ratio: ratio, tokens: Reserve, now: time.Now}
	b.last = b.now()
	return b
}

// SetClock replaces the budget's clock; tests only.
func (b *Budget) SetClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
	b.last = now()
}

// Deposit credits the budget for a request's first upstream attempt.
func (b *Budget) Deposit() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	b.tokens = min(Reserve, b.tokens+b.ratio)
}

// Withdraw takes one retry and reports whether the budget held one.
func (b *Budget) Withdraw() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Refund returns a withdrawn retry that was not made.
func (b *Budget) Refund() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens = min(Reserve, b.tokens+1)
}

func (b *Budget) refill() {
	now := b.now()
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(Reserve, b.tokens+elapsed.Seconds()*RefillPerSecond)
		b.last = now
	}
}

// Backoff is the full-jitter delay before the nth retry of one backend
// (n ≥ 1): u, in [0, 1), times min(BackoffCap, BackoffBase·2^(n−1)).
func Backoff(n int, u float64) time.Duration {
	ceiling := BackoffCap
	if n >= 1 && n <= 16 {
		ceiling = min(BackoffCap, BackoffBase<<(n-1))
	}
	return time.Duration(u * float64(ceiling))
}
