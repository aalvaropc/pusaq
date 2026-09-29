// Package spend keeps each tenant's monthly spend in Redis so a policy can
// cut traffic off when a budget is exhausted. The counter is fed with the
// priced cost of every completion and read before admission; it is not a
// reservation, so in-flight requests bound the overrun.
package spend

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultTTL keeps a month's counter alive well past the month it covers.
const DefaultTTL = 40 * 24 * time.Hour

// addScript increments the month's total and refreshes its expiry in one
// atomic operation; INCRBYFLOAT returns the new total as a string.
const addScript = `
local total = redis.call('INCRBYFLOAT', KEYS[1], ARGV[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
return total
`

// Counter accumulates spend per tenant and UTC month.
type Counter struct {
	client redis.Cmdable
	add    *redis.Script
	ttl    time.Duration
}

// New builds a Counter on the given client.
func New(client redis.Cmdable) *Counter {
	return &Counter{client: client, add: redis.NewScript(addScript), ttl: DefaultTTL}
}

// Key names a tenant's counter for the UTC month containing at. The hash
// tag keeps a tenant's keys on one slot, like the rate-limit buckets.
func Key(tenant string, at time.Time) string {
	return fmt.Sprintf("spend:{%s}:%s", tenant, at.UTC().Format("2006-01"))
}

// NextMonth is the first instant of the UTC month after at — when a
// budget resets.
func NextMonth(at time.Time) time.Time {
	u := at.UTC()
	return time.Date(u.Year(), u.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// Add records usd of spend for the month containing at and returns the
// month's new total.
func (c *Counter) Add(ctx context.Context, tenant string, at time.Time, usd float64) (float64, error) {
	s, err := c.add.Run(ctx, c.client, []string{Key(tenant, at)}, usd, int(c.ttl.Seconds())).Text()
	if err != nil {
		return 0, fmt.Errorf("spend add: %w", err)
	}
	total, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("spend add returned %q: %w", s, err)
	}
	return total, nil
}

// Month reads the month's total; a missing counter is zero.
func (c *Counter) Month(ctx context.Context, tenant string, at time.Time) (float64, error) {
	s, err := c.client.Get(ctx, Key(tenant, at)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("spend read: %w", err)
	}
	total, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("spend counter holds %q: %w", s, err)
	}
	return total, nil
}
