// Package storegate stops an instance from calling its limit store while the
// store keeps failing: a circuit breaker counts the store's answers, and while
// it is open every call fails at once until a background PING is answered.
package storegate

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/aalvaropc/pusaq/internal/breaker"
)

// Rule and probe parameters (docs/design.md §5.2).
const (
	Window           = 20
	MinSamples       = 10
	FailureThreshold = 0.5
	Cooldown         = time.Second
	ProbeBudget      = 250 * time.Millisecond
)

// ErrOpen is the error of a call the open gate did not send.
var ErrOpen = errors.New("store gate open: call not sent")

type passKey struct{}

// Gate is a redis.Hook that skips store calls while its breaker is open.
type Gate struct {
	b     *breaker.Breaker
	ping  func(context.Context) error
	spawn func(func())
}

// New builds a closed Gate whose probe runs ping; onChange receives every transition.
func New(ping func(context.Context) error, onChange func(from, to breaker.State)) *Gate {
	return &Gate{
		b: breaker.New(breaker.Config{
			Window:           Window,
			MinSamples:       MinSamples,
			FailureThreshold: FailureThreshold,
			Cooldown:         Cooldown,
			Probes:           1,
			OnStateChange:    onChange,
		}),
		ping:  ping,
		spawn: func(f func()) { go f() },
	}
}

// Attach guards client with a new Gate probed by PING.
func Attach(client *redis.Client, onChange func(from, to breaker.State)) *Gate {
	g := New(func(ctx context.Context) error { return client.Ping(ctx).Err() }, onChange)
	client.AddHook(g)
	return g
}

// State reports the gate's current state.
func (g *Gate) State() breaker.State { return g.b.State() }

// DialHook implements redis.Hook.
func (g *Gate) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook implements redis.Hook.
func (g *Gate) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return g.do(ctx, func(ctx context.Context) error { return next(ctx, cmd) }, cmd.SetErr)
	}
}

// ProcessPipelineHook implements redis.Hook.
func (g *Gate) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		return g.do(ctx, func(ctx context.Context) error { return next(ctx, cmds) }, func(err error) {
			for _, cmd := range cmds {
				cmd.SetErr(err)
			}
		})
	}
}

func (g *Gate) do(ctx context.Context, call func(context.Context) error, skip func(error)) error {
	if ctx.Value(passKey{}) != nil {
		return call(ctx)
	}
	t, ok := g.b.Allow()
	if ok && t.Probe() {
		g.spawn(func() { g.probe(t) })
		ok = false
	}
	if !ok {
		skip(ErrOpen)
		return ErrOpen
	}
	err := call(context.WithValue(ctx, passKey{}, true))
	switch {
	case answered(err):
		t.Record(true)
	case errors.Is(ctx.Err(), context.Canceled):
		t.Release()
	default:
		t.Record(false)
	}
	return err
}

func (g *Gate) probe(t breaker.Ticket) {
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), passKey{}, true), ProbeBudget)
	defer cancel()
	t.Record(answered(g.ping(ctx)))
}

func answered(err error) bool {
	var reply redis.Error
	return err == nil || errors.As(err, &reply)
}
