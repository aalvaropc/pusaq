// Package ratelimit implements Pusaq's distributed token bucket: one bucket
// per tenant in Redis, decided by a single atomic Lua script shared by all
// gateway replicas (docs/design.md §5.1–5.2). Read-decide-decrement happens
// as one operation inside Redis — the naive three-step version is a race by
// construction.
package ratelimit

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

const refillBucket = `
local B = tonumber(ARGV[1])
local r = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local st = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(st[1]) or B
local ts = tonumber(st[2]) or now
local rollback = math.max(0, ts - now)
now = math.max(now, ts)
tokens = math.min(B, tokens + (now - ts) / 1000 * r)
`

const persistBucket = `
redis.call('HMSET', KEYS[1], 'tokens', tokens, 'ts', now)
redis.call('EXPIRE', KEYS[1], ttl)
`

const script = refillBucket + `
local cost = tonumber(ARGV[4])
local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
end
` + persistBucket + `
local retry = 0
if allowed == 0 then
  retry = rollback + math.ceil((cost - tokens) / r * 1000)
end
return {allowed, tostring(tokens), retry}
`

const adjustScript = refillBucket + `
local delta = tonumber(ARGV[4])
tokens = math.min(B, tokens + delta)
` + persistBucket + `
return tostring(tokens)
`

const takeScript = refillBucket + `
local need = tonumber(ARGV[4])
local want = tonumber(ARGV[5])
local granted = 0
if tokens >= need then
  granted = math.max(need, math.min(want, math.floor(tokens)))
  tokens = tokens - granted
end
` + persistBucket + `
local retry = 0
if granted == 0 then
  retry = rollback + math.ceil((need - tokens) / r * 1000)
end
return {tostring(granted), tostring(tokens), retry}
`

// Limits parameterize one bucket dimension.
type Limits struct {
	// Burst is the bucket capacity B: the largest instantaneous burst.
	Burst float64
	// Rate is the sustained refill in tokens per second.
	Rate float64
	// TTL reclaims idle buckets; an expired bucket starts full again.
	TTL time.Duration
}

// Validate rejects meaningless limits.
func (l Limits) Validate() error {
	if !(l.Burst > 0) || !(l.Rate > 0) || math.IsInf(l.Burst, 0) || math.IsInf(l.Rate, 0) {
		return fmt.Errorf("burst and rate must be positive, got burst=%g rate=%g", l.Burst, l.Rate)
	}
	if l.TTL <= 0 {
		return fmt.Errorf("ttl must be positive, got %s", l.TTL)
	}
	return nil
}

// Decision is the limiter's verdict for one request.
type Decision struct {
	Allowed   bool
	Remaining float64
	// RetryAfter is how long until the required tokens accrue; zero when
	// allowed.
	RetryAfter time.Duration
}

// Grant is the result of taking a batch of tokens.
type Grant struct {
	// Granted is how many tokens were taken (zero when denied).
	Granted float64
	// Remaining is what the bucket holds after the take.
	Remaining float64
	// RetryAfter is how long until `need` tokens accrue; zero when granted.
	RetryAfter time.Duration
}

// Limiter decides admissions against a shared Redis store.
type Limiter struct {
	client redis.Scripter
	limits Limits
	prefix string
	script *redis.Script
	adjust *redis.Script
	take   *redis.Script
}

// New builds a Limiter for one dimension (e.g. "rpm") with the given limits.
func New(client redis.Scripter, prefix string, limits Limits) (*Limiter, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Limiter{
		client: client,
		limits: limits,
		prefix: prefix,
		script: redis.NewScript(script),
		adjust: redis.NewScript(adjustScript),
		take:   redis.NewScript(takeScript),
	}, nil
}

// Limits reports the bucket parameters.
func (l *Limiter) Limits() Limits { return l.limits }

// Allow runs the atomic decision for one tenant. An error means the store
// was unreachable — the caller decides the degraded-mode policy (the
// gateway fails open).
func (l *Limiter) Allow(ctx context.Context, tenant string, cost float64) (Decision, error) {
	key := fmt.Sprintf("rl:{%s}:%s", tenant, l.prefix)
	res, err := l.script.Run(ctx, l.client, []string{key},
		l.limits.Burst,
		l.limits.Rate,
		int(l.limits.TTL.Seconds()),
		cost,
	).Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("rate limit script: %w", err)
	}
	if len(res) != 3 {
		return Decision{}, fmt.Errorf("rate limit script returned %d values", len(res))
	}

	allowed, _ := res[0].(int64)
	var remaining float64
	if s, ok := res[1].(string); ok {
		_, _ = fmt.Sscanf(s, "%g", &remaining)
	}
	retryMS, _ := res[2].(int64)

	return Decision{
		Allowed:    allowed == 1,
		Remaining:  remaining,
		RetryAfter: time.Duration(retryMS) * time.Millisecond,
	}, nil
}

// Adjust reconciles a tenant's bucket once actual consumption is known:
// positive delta refunds unused reservation (capped at capacity), negative
// charges the shortfall. Atomic like Allow; an error means the store was
// unreachable and the caller may accept the leaked charge (bounded by TTL).
func (l *Limiter) Adjust(ctx context.Context, tenant string, delta float64) error {
	key := fmt.Sprintf("rl:{%s}:%s", tenant, l.prefix)
	err := l.adjust.Run(ctx, l.client, []string{key},
		l.limits.Burst,
		l.limits.Rate,
		int(l.limits.TTL.Seconds()),
		delta,
	).Err()
	if err != nil {
		return fmt.Errorf("rate limit adjust script: %w", err)
	}
	return nil
}

// Take atomically takes between need and want tokens from a tenant's bucket
// (never more than it holds, never a fraction). A zero Granted means the
// bucket could not cover need; RetryAfter says when it will. An error means
// the store was unreachable.
func (l *Limiter) Take(ctx context.Context, tenant string, need, want float64) (Grant, error) {
	key := fmt.Sprintf("rl:{%s}:%s", tenant, l.prefix)
	res, err := l.take.Run(ctx, l.client, []string{key},
		l.limits.Burst,
		l.limits.Rate,
		int(l.limits.TTL.Seconds()),
		need,
		want,
	).Slice()
	if err != nil {
		return Grant{}, fmt.Errorf("rate limit take script: %w", err)
	}
	if len(res) != 3 {
		return Grant{}, fmt.Errorf("rate limit take script returned %d values", len(res))
	}
	var granted, remaining float64
	if s, ok := res[0].(string); ok {
		_, _ = fmt.Sscanf(s, "%g", &granted)
	}
	if s, ok := res[1].(string); ok {
		_, _ = fmt.Sscanf(s, "%g", &remaining)
	}
	retryMS, _ := res[2].(int64)
	return Grant{
		Granted:    granted,
		Remaining:  remaining,
		RetryAfter: time.Duration(retryMS) * time.Millisecond,
	}, nil
}
