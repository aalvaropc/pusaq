// Package ratelimit implements Pusaq's distributed token bucket: one bucket
// per tenant in Redis, decided by a single atomic Lua script shared by all
// gateway replicas (docs/design.md §5.1–5.2). Read-decide-decrement happens
// as one operation inside Redis — the naive three-step version is a race by
// construction.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// script is the heart of the limiter. Atomicity comes from Redis executing
// Lua as one operation; refill is computed lazily from elapsed time (no
// background refiller). The bucket starts full so a new tenant can use its
// burst immediately. `now` comes from the replica: redis.TIME would remove
// the replica-clock dependence but makes the script non-deterministic for
// replication — the founding doc's documented trade-off.
//
// KEYS[1] = bucket key. ARGV: capacity(B), rate(r tokens/sec), now(ms),
// cost, ttl(sec). Returns {allowed, remaining, retry_after_ms}.
const script = `
local B    = tonumber(ARGV[1])
local r    = tonumber(ARGV[2])
local now  = tonumber(ARGV[3])
local cost = tonumber(ARGV[4])
local ttl  = tonumber(ARGV[5])

local st     = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(st[1])
local ts     = tonumber(st[2])
if tokens == nil then
  tokens, ts = B, now
end

local delta = math.max(0, now - ts) / 1000.0
tokens = math.min(B, tokens + delta * r)

local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
end

redis.call('HMSET', KEYS[1], 'tokens', tokens, 'ts', now)
redis.call('EXPIRE', KEYS[1], ttl)

local retry = 0
if allowed == 0 then
  retry = math.ceil((cost - tokens) / r * 1000)
end
return { allowed, tostring(tokens), retry }
`

// adjustScript reconciles a bucket after actual consumption is known:
// refill by elapsed time, then apply the delta (positive = refund, negative
// = extra charge), capped at capacity. Refunds can never push a bucket past
// full; extra charges may go negative, pushing the next refill window out.
//
// KEYS[1] = bucket key. ARGV: capacity(B), rate(r), now(ms), delta, ttl.
const adjustScript = `
local B     = tonumber(ARGV[1])
local r     = tonumber(ARGV[2])
local now   = tonumber(ARGV[3])
local delta = tonumber(ARGV[4])
local ttl   = tonumber(ARGV[5])

local st     = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(st[1])
local ts     = tonumber(st[2])
if tokens == nil then
  tokens, ts = B, now
end

local elapsed = math.max(0, now - ts) / 1000.0
tokens = math.min(B, tokens + elapsed * r)
tokens = math.min(B, tokens + delta)

redis.call('HMSET', KEYS[1], 'tokens', tokens, 'ts', now)
redis.call('EXPIRE', KEYS[1], ttl)
return tostring(tokens)
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
	if l.Burst <= 0 || l.Rate <= 0 {
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

// Limiter decides admissions against a shared Redis store.
type Limiter struct {
	client redis.Scripter
	limits Limits
	prefix string
	script *redis.Script
	adjust *redis.Script
	// now is injected so tests freeze or advance time deterministically.
	now func() time.Time
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
		now:    time.Now,
	}, nil
}

// Allow runs the atomic decision for one tenant. An error means the store
// was unreachable — the caller decides the degraded-mode policy (the
// gateway fails open).
func (l *Limiter) Allow(ctx context.Context, tenant string, cost float64) (Decision, error) {
	key := fmt.Sprintf("rl:{%s}:%s", tenant, l.prefix)
	res, err := l.script.Run(ctx, l.client, []string{key},
		l.limits.Burst,
		l.limits.Rate,
		l.now().UnixMilli(),
		cost,
		int(l.limits.TTL.Seconds()),
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
		l.now().UnixMilli(),
		delta,
		int(l.limits.TTL.Seconds()),
	).Err()
	if err != nil {
		return fmt.Errorf("rate limit adjust script: %w", err)
	}
	return nil
}
