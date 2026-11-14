package gateway

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/retry"
)

// Retries bounds same-backend retries; the zero value retries nothing.
type Retries struct {
	Max    int
	Window time.Duration
	Budget *retry.Budget

	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	jitter func() float64
}

type retryRun struct {
	Retries
	req      *core.Request
	deadline time.Time
}

func (r Retries) start(req *core.Request) *retryRun {
	if r.now == nil {
		r.now = time.Now
	}
	if r.sleep == nil {
		r.sleep = sleepContext
	}
	if r.jitter == nil {
		r.jitter = rand.Float64
	}
	return &retryRun{Retries: r, req: req}
}

func (r *retryRun) attempt(ctx context.Context, isRetry bool) {
	if r.req.Attempts == 0 && r.Max > 0 {
		r.deadline = r.now().Add(r.Window)
		if d, ok := ctx.Deadline(); ok && d.Before(r.deadline) {
			r.deadline = d
		}
		if r.Budget != nil {
			r.Budget.Deposit()
		}
	}
	r.req.Attempts++
	if isRetry {
		r.req.Retries++
	}
}

func (r *retryRun) next(err error, backendRetries int) (time.Duration, bool) {
	if r.Max <= 0 || r.req.Retries >= r.Max {
		return 0, false
	}
	re, ok := errors.AsType[*provider.RetryableError](err)
	if !ok {
		return 0, false
	}
	wait := retry.Backoff(backendRetries+1, r.jitter())
	if re.RetryAfter > 0 {
		wait = re.RetryAfter
	}
	if !r.now().Add(wait).Before(r.deadline) {
		r.deny("deadline")
		return 0, false
	}
	if r.Budget != nil && !r.Budget.Withdraw() {
		r.deny("budget")
		return 0, false
	}
	return wait, true
}

func (r *retryRun) unspend() {
	if r.Budget != nil {
		r.Budget.Refund()
	}
}

func (r *retryRun) deny(reason string) {
	if !slices.Contains(r.req.RetryDenied, reason) {
		r.req.RetryDenied = append(r.req.RetryDenied, reason)
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
