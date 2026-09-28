package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/spend"
)

// Policy is what a tenant may do. The zero value restricts nothing: a
// tenant without a policy is unrestricted (docs/design.md §4).
type Policy struct {
	// Models, when non-nil, is the set of concrete aliases the tenant may be
	// served by; pusaq/auto is checked against the alias it resolves to.
	Models map[string]bool
	// MaxPromptChars caps the bytes of message content; zero means no cap.
	MaxPromptChars int
	// BudgetUSD is the monthly spend ceiling; zero means no budget.
	BudgetUSD float64
	// FailClosed rejects requests when a limit's store is unreachable,
	// instead of the default fail-open.
	FailClosed bool
}

// PolicyFor resolves a tenant's policy; nil disables policies entirely.
type PolicyFor func(tenant string) Policy

// SpendCounter tracks monthly spend per tenant. Satisfied by
// *spend.Counter; nil disables budgets.
type SpendCounter interface {
	Month(ctx context.Context, tenant string, at time.Time) (float64, error)
	Add(ctx context.Context, tenant string, at time.Time, usd float64) (float64, error)
}

// policyFor resolves the tenant's policy, tolerating a nil registry.
func (o CompletionsOptions) policyFor(tenant string) Policy {
	if o.Policies == nil {
		return Policy{}
	}
	return o.Policies(tenant)
}

// promptChars is the size of the prompt as bytes of message content.
func promptChars(messages []provider.Message) int {
	n := 0
	for _, m := range messages {
		n += len(m.Content)
	}
	return n
}

// applyPolicy enforces the tenant's policy after the alias is resolved and
// before any token is charged: allowed models on the concrete alias, the
// prompt cap, and the monthly budget. A budgeted request registers the
// finalizer that adds its priced cost to the month's spend. The cost event
// records the denial code; nothing else is logged per request.
func applyPolicy(ctx context.Context, req *core.Request, cr completionRequest, alias string, opts CompletionsOptions) error {
	p := opts.policyFor(req.Tenant)

	if p.Models != nil && !p.Models[alias] {
		if alias != cr.Model {
			return core.Errorf(core.CodePolicyDenied, "model %q (resolved from %q) is not allowed for this tenant", alias, cr.Model)
		}
		return core.Errorf(core.CodePolicyDenied, "model %q is not allowed for this tenant", alias)
	}
	if p.MaxPromptChars > 0 {
		if n := promptChars(cr.Messages); n > p.MaxPromptChars {
			return core.Errorf(core.CodePolicyDenied, "prompt of %d chars exceeds the tenant limit of %d", n, p.MaxPromptChars)
		}
	}
	if p.BudgetUSD <= 0 || opts.Spend == nil {
		return nil
	}

	limCtx, cancel := context.WithTimeout(ctx, limiterBudget)
	spent, err := opts.Spend.Month(limCtx, req.Tenant, req.ArrivedAt)
	cancel()
	if err != nil {
		if p.FailClosed {
			opts.Logger.Warn("spend store unavailable, failing closed",
				slog.String("request_id", req.ID), slog.String("tenant", req.Tenant), slog.Any("error", err))
			return &core.Error{Code: core.CodeStoreUnavailable, Message: "spend store unavailable", Err: err}
		}
		opts.Logger.Warn("spend store unavailable, failing open",
			slog.String("request_id", req.ID), slog.String("tenant", req.Tenant), slog.Any("error", err))
	} else if spent >= p.BudgetUSD {
		req.SpendUSDMonth = spent
		return &core.Error{
			Code:       core.CodeBudgetExceeded,
			Message:    fmt.Sprintf("monthly budget of %.2f USD exhausted", p.BudgetUSD),
			RetryAfter: time.Until(spend.NextMonth(req.ArrivedAt)),
		}
	}

	// Registered after the pricing finalizer, so req.Cost is set when this
	// runs — for buffered responses and at stream end alike.
	tenant, at := req.Tenant, req.ArrivedAt
	req.OnFinalize(func() {
		if req.Cost.USD <= 0 {
			return
		}
		addCtx, cancel := context.WithTimeout(context.Background(), limiterBudget)
		defer cancel()
		total, err := opts.Spend.Add(addCtx, tenant, at, req.Cost.USD)
		if err != nil {
			opts.Logger.Warn("spend counter update failed; spend under-counted",
				slog.String("request_id", req.ID), slog.String("tenant", tenant), slog.Any("error", err))
			return
		}
		req.SpendUSDMonth = total
	})
	return nil
}
