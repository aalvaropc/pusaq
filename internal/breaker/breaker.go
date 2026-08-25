// Package breaker implements the circuit breaker that guards each upstream
// backend (docs/design.md §5.6): closed / open / half-open. It is pure state
// with no I/O — callers ask Allow before an attempt and Record the outcome
// after — so the whole machine is deterministic under an injected clock.
package breaker

import (
	"sync"
	"time"
)

// State is the breaker's current position.
type State string

const (
	// StateClosed lets traffic flow while counting outcomes.
	StateClosed State = "closed"
	// StateOpen fails fast: no attempts reach the backend.
	StateOpen State = "open"
	// StateHalfOpen lets a bounded number of probes decide recovery.
	StateHalfOpen State = "half_open"
)

// Config parameterizes one breaker. Zero values fall back to defaults.
type Config struct {
	// Window is how many recent outcomes the failure rate is computed over.
	Window int
	// MinSamples gates opening: below it, even 100% failures keep the
	// breaker closed — 2 failures out of 2 requests is not a signal.
	MinSamples int
	// FailureThreshold opens the breaker when failures/count exceeds it.
	FailureThreshold float64
	// Cooldown is how long the breaker stays open before probing.
	Cooldown time.Duration
	// Probes is the maximum concurrent half-open probe attempts.
	Probes int
}

// Defaults applied by New for zero fields.
const (
	DefaultWindow           = 20
	DefaultMinSamples       = 10
	DefaultFailureThreshold = 0.5
	DefaultCooldown         = 10 * time.Second
	DefaultProbes           = 3
)

func (c Config) withDefaults() Config {
	if c.Window <= 0 {
		c.Window = DefaultWindow
	}
	if c.MinSamples <= 0 {
		c.MinSamples = DefaultMinSamples
	}
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = DefaultFailureThreshold
	}
	if c.Cooldown <= 0 {
		c.Cooldown = DefaultCooldown
	}
	if c.Probes <= 0 {
		c.Probes = DefaultProbes
	}
	return c
}

// Breaker guards one backend. Safe for concurrent use.
type Breaker struct {
	mu sync.Mutex

	cfg   Config
	state State
	// outcomes is a ring of the last cfg.Window results (true = failure).
	outcomes []bool
	next     int
	count    int
	failures int

	openedAt time.Time
	probes   int

	// now is injected so tests control cooldown deterministically.
	now func() time.Time
}

// New builds a closed Breaker with cfg (zero fields take defaults).
func New(cfg Config) *Breaker {
	cfg = cfg.withDefaults()
	return &Breaker{
		cfg:      cfg,
		state:    StateClosed,
		outcomes: make([]bool, cfg.Window),
		now:      time.Now,
	}
}

// SetClock replaces the breaker's clock; tests only.
func (b *Breaker) SetClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
}

// State reports the current state (after applying cooldown transitions).
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpen()
	return b.state
}

// Allow reports whether an attempt may proceed now. In half-open it grants
// at most cfg.Probes concurrent probes; callers MUST call Record for every
// allowed attempt, or probe slots leak.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpen()

	switch b.state {
	case StateClosed:
		return true
	case StateHalfOpen:
		if b.probes < b.cfg.Probes {
			b.probes++
			return true
		}
		return false
	default: // StateOpen
		return false
	}
}

// Record feeds an attempt's outcome back into the machine.
func (b *Breaker) Record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateHalfOpen:
		if b.probes > 0 {
			b.probes--
		}
		if success {
			// Recovered: close and start a fresh window.
			b.toClosed()
			return
		}
		b.toOpen()
		return
	case StateOpen:
		// A straggler from before the breaker opened; ignore.
		return
	}

	// Closed: slide the window.
	if b.count == b.cfg.Window {
		if b.outcomes[b.next] {
			b.failures--
		}
	} else {
		b.count++
	}
	b.outcomes[b.next] = !success
	if !success {
		b.failures++
	}
	b.next = (b.next + 1) % b.cfg.Window

	if b.count >= b.cfg.MinSamples &&
		float64(b.failures)/float64(b.count) > b.cfg.FailureThreshold {
		b.toOpen()
	}
}

// maybeHalfOpen transitions open → half-open once the cooldown elapsed.
// Callers hold b.mu.
func (b *Breaker) maybeHalfOpen() {
	if b.state == StateOpen && b.now().Sub(b.openedAt) >= b.cfg.Cooldown {
		b.state = StateHalfOpen
		b.probes = 0
	}
}

// toOpen resets to the open state. Callers hold b.mu.
func (b *Breaker) toOpen() {
	b.state = StateOpen
	b.openedAt = b.now()
	b.probes = 0
}

// toClosed resets to a fresh closed state. Callers hold b.mu.
func (b *Breaker) toClosed() {
	b.state = StateClosed
	b.count = 0
	b.failures = 0
	b.next = 0
	b.probes = 0
	for i := range b.outcomes {
		b.outcomes[i] = false
	}
}
