package spend

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newCounter(t *testing.T) (*Counter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return New(client), mr
}

func TestAddAccumulatesAndReturnsTotal(t *testing.T) {
	c, _ := newCounter(t)
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	if total, err := c.Add(context.Background(), "team-a", at, 0.5); err != nil || total != 0.5 {
		t.Fatalf("first add = %v, %v", total, err)
	}
	if total, err := c.Add(context.Background(), "team-a", at, 0.25); err != nil || total != 0.75 {
		t.Fatalf("second add = %v, %v", total, err)
	}
	if got, err := c.Month(context.Background(), "team-a", at); err != nil || got != 0.75 {
		t.Fatalf("month = %v, %v", got, err)
	}
}

func TestMonthMissingKeyIsZero(t *testing.T) {
	c, _ := newCounter(t)
	if got, err := c.Month(context.Background(), "nobody", time.Now()); err != nil || got != 0 {
		t.Fatalf("month = %v, %v, want 0", got, err)
	}
}

func TestKeyIsUTCMonth(t *testing.T) {
	lima := time.FixedZone("Lima", -5*3600)
	at := time.Date(2026, 9, 30, 23, 30, 0, 0, lima) // already October in UTC
	if got := Key("t", at); got != "spend:{t}:2026-10" {
		t.Fatalf("key = %q", got)
	}
	c, _ := newCounter(t)
	sept := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	octo := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	_, _ = c.Add(context.Background(), "t", sept, 1)
	_, _ = c.Add(context.Background(), "t", octo, 2)
	if s, _ := c.Month(context.Background(), "t", sept); s != 1 {
		t.Fatalf("september = %v, want 1", s)
	}
	if o, _ := c.Month(context.Background(), "t", octo); o != 2 {
		t.Fatalf("october = %v, want 2", o)
	}
}

func TestAddSetsTTL(t *testing.T) {
	c, mr := newCounter(t)
	at := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if _, err := c.Add(context.Background(), "t", at, 0.1); err != nil {
		t.Fatal(err)
	}
	if ttl := mr.TTL(Key("t", at)); math.Abs(float64(ttl-DefaultTTL)) > float64(time.Second) {
		t.Fatalf("ttl = %s, want %s", ttl, DefaultTTL)
	}
}

func TestNextMonth(t *testing.T) {
	if got := NextMonth(time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("next month = %s", got)
	}
	if got := NextMonth(time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC)); !got.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("next month across year = %s", got)
	}
}
