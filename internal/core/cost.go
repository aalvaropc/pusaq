package core

import "time"

// Outcome classifies how a request ended, for cost accounting.
type Outcome string

const (
	// OutcomeSuccess marks a request that completed normally.
	OutcomeSuccess Outcome = "success"
	// OutcomeError marks a request that ended in a normalized error.
	OutcomeError Outcome = "error"
	// OutcomePanic marks a request whose pipeline panicked and was recovered.
	OutcomePanic Outcome = "panic"
)

// CostEvent is the per-request accounting record — the raw material of
// Pusaq's cost accounting (design.md §5.7). Exactly one event is emitted per
// admitted request, regardless of outcome. The schema grows with later
// slices (tenant, model, tokens, price) without breaking consumers.
type CostEvent struct {
	RequestID string        `json:"request_id"`
	Timestamp time.Time     `json:"timestamp"`
	Op        string        `json:"op"`
	Tenant    string        `json:"tenant,omitempty"`
	Outcome   Outcome       `json:"outcome"`
	ErrorCode Code          `json:"error_code,omitempty"`
	Latency   time.Duration `json:"latency_ms"`

	// RequestedModel and ServedModel are set for operations that resolve
	// models (empty otherwise); Usage carries provider-reported tokens.
	RequestedModel string `json:"requested_model,omitempty"`
	ServedModel    string `json:"served_model,omitempty"`
	Usage          Usage  `json:"usage,omitzero"`
	// TokensCharged is the admission charge (input estimate + output
	// reservation) so reservation error is measurable against Usage.
	TokensCharged int `json:"tokens_charged,omitempty"`
}
