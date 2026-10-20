package ratelimit

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// sweepBudget bounds one sweep of refunds, by the janitor or by Close.
const sweepBudget = 250 * time.Millisecond

// Leased trades per-window exactness for fewer store round-trips: each
// replica takes a lease of up to K tokens from the shared bucket in one
// atomic operation and admits up to K requests locally. Cumulative
// admissions from a full bucket never exceed the bucket (tokens are taken
// before they are spent); within a window the admitted count can exceed
// strict accounting by at most M·K across M replicas, because tokens held
// in leases are counted as spent by the store. Unused tokens go back on
// lease expiry and on Close; a replica that dies without closing loses at
// most K tokens per tenant, which under-admits and never overruns. Denials
// are cached locally until the store-computed retry time, so an overloaded
// tenant costs one round-trip per retry interval instead of one per
// request.
//
// Leased satisfies the same Allow surface as Limiter; it is meant for the
// request dimension, whose cost is always 1.
type Leased struct {
	inner *Limiter
	size  float64
	ttl   time.Duration
	// now is injected so tests control expiry deterministically.
	now func() time.Time

	mu     sync.Mutex
	leases map[string]*lease

	acquisitions   atomic.Uint64
	localAdmits    atomic.Uint64
	localDenies    atomic.Uint64
	refunds        atomic.Uint64
	refundFailures atomic.Uint64
	lostMu         sync.Mutex
	tokensLost     float64

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// lease is one tenant's locally held tokens on this replica.
type lease struct {
	mu sync.Mutex
	// tokens are admissions available locally until expires.
	tokens  float64
	expires time.Time
	// pending are tokens waiting to be refunded to the store.
	pending float64
	// denyUntil caches a store denial: no round-trip before it.
	denyUntil time.Time
}

// LeaseStats are the counters a lease-mode limiter exposes at shutdown.
type LeaseStats struct {
	// Acquisitions counts store round-trips made to take leases (grants
	// and denials alike).
	Acquisitions uint64
	// LocalAdmits and LocalDenies count decisions served without the store.
	LocalAdmits uint64
	LocalDenies uint64
	// Refunds counts successful returns of unused tokens; RefundFailures
	// counts refunds the store did not accept, whose tokens are TokensLost.
	Refunds        uint64
	RefundFailures uint64
	TokensLost     float64
}

// NewLeased wraps a Limiter in lease mode with leases of size tokens that
// expire after ttl. A janitor goroutine refunds expired leases; Close stops
// it and refunds everything still held.
func NewLeased(inner *Limiter, size int, ttl time.Duration) *Leased {
	l := &Leased{
		inner:  inner,
		size:   float64(size),
		ttl:    ttl,
		now:    time.Now,
		leases: make(map[string]*lease),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go l.janitor()
	return l
}

// Allow admits from the local lease when it can, and takes a new lease from
// the store when it cannot. An error means the store was unreachable — the
// caller decides the degraded-mode policy (the gateway fails open).
func (l *Leased) Allow(ctx context.Context, tenant string, cost float64) (Decision, error) {
	ls := l.leaseFor(tenant)
	ls.mu.Lock()
	defer ls.mu.Unlock()

	now := l.now()
	if ls.tokens > 0 && !now.Before(ls.expires) {
		ls.pending += ls.tokens
		ls.tokens = 0
	}
	if ls.tokens >= cost {
		ls.tokens -= cost
		l.localAdmits.Add(1)
		return Decision{Allowed: true, Remaining: ls.tokens}, nil
	}
	if now.Before(ls.denyUntil) {
		l.localDenies.Add(1)
		return Decision{Allowed: false, RetryAfter: ls.denyUntil.Sub(now)}, nil
	}
	// A leftover smaller than cost goes back rather than being topped up:
	// one take, one whole lease.
	if ls.tokens > 0 {
		ls.pending += ls.tokens
		ls.tokens = 0
	}

	g, err := l.inner.Take(ctx, tenant, cost, math.Max(cost, l.size))
	if err != nil {
		return Decision{}, err
	}
	l.acquisitions.Add(1)
	if g.Granted == 0 {
		ls.denyUntil = now.Add(min(g.RetryAfter, l.ttl))
		return Decision{Allowed: false, Remaining: g.Remaining, RetryAfter: g.RetryAfter}, nil
	}
	ls.tokens = g.Granted - cost
	ls.expires = now.Add(l.ttl)
	return Decision{Allowed: true, Remaining: ls.tokens}, nil
}

// Stats returns a snapshot of the counters.
func (l *Leased) Stats() LeaseStats {
	l.lostMu.Lock()
	lost := l.tokensLost
	l.lostMu.Unlock()
	return LeaseStats{
		Acquisitions:   l.acquisitions.Load(),
		LocalAdmits:    l.localAdmits.Load(),
		LocalDenies:    l.localDenies.Load(),
		Refunds:        l.refunds.Load(),
		RefundFailures: l.refundFailures.Load(),
		TokensLost:     lost,
	}
}

// Close stops the janitor and refunds every token still held, expired or
// not, so a draining replica leaves nothing stranded.
func (l *Leased) Close() {
	l.stopOnce.Do(func() {
		close(l.stop)
		<-l.done
		ctx, cancel := context.WithTimeout(context.Background(), sweepBudget)
		defer cancel()
		l.sweep(ctx, true)
	})
}

func (l *Leased) leaseFor(tenant string) *lease {
	l.mu.Lock()
	defer l.mu.Unlock()
	ls, ok := l.leases[tenant]
	if !ok {
		ls = &lease{}
		l.leases[tenant] = ls
	}
	return ls
}

// janitor refunds expired leases twice per TTL so tokens a quiet tenant
// stopped using return to the shared bucket promptly.
func (l *Leased) janitor() {
	defer close(l.done)
	interval := max(l.ttl/2, 10*time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), sweepBudget)
			l.sweep(ctx, false)
			cancel()
		}
	}
}

// sweep moves expired (or, when all is set, every) locally held token to
// pending and refunds pending tokens to the store, one round-trip per
// tenant with something to return. Refund failures are counted and their
// tokens recorded as lost: a bounded, visible under-admission.
func (l *Leased) sweep(ctx context.Context, all bool) {
	type due struct {
		tenant string
		ls     *lease
	}
	l.mu.Lock()
	snapshot := make([]due, 0, len(l.leases))
	for tenant, ls := range l.leases {
		snapshot = append(snapshot, due{tenant, ls})
	}
	l.mu.Unlock()

	now := l.now()
	for _, d := range snapshot {
		d.ls.mu.Lock()
		if d.ls.tokens > 0 && (all || !now.Before(d.ls.expires)) {
			d.ls.pending += d.ls.tokens
			d.ls.tokens = 0
		}
		p := d.ls.pending
		d.ls.pending = 0
		idle := d.ls.tokens == 0 && !now.Before(d.ls.denyUntil)
		d.ls.mu.Unlock()

		if p > 0 {
			if err := l.inner.Adjust(ctx, d.tenant, p); err != nil {
				l.refundFailures.Add(1)
				l.lostMu.Lock()
				l.tokensLost += p
				l.lostMu.Unlock()
			} else {
				l.refunds.Add(1)
			}
		}
		if idle {
			// Forget quiet tenants so the map does not grow without bound;
			// the next request simply creates a fresh entry.
			l.mu.Lock()
			if cur := l.leases[d.tenant]; cur == d.ls {
				d.ls.mu.Lock()
				if d.ls.tokens == 0 && d.ls.pending == 0 {
					delete(l.leases, d.tenant)
				}
				d.ls.mu.Unlock()
			}
			l.mu.Unlock()
		}
	}
}
