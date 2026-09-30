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
// Pusaq's cost accounting (docs/design.md §5.7). Exactly one event is emitted per
// admitted request, regardless of outcome. The schema can grow with new
// fields (e.g. price) without breaking consumers.
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
	// FeatureTag is the request's declared feature; RouteReason records
	// how the requested alias resolved (direct, rule:<tag>, default:…).
	FeatureTag  string `json:"feature_tag,omitempty"`
	RouteReason string `json:"route_reason,omitempty"`
	// RunID is the client-declared experiment run (metadata.run_id);
	// Region labels the replica's region (PUSAQ_REGION). Both are empty
	// when unset and let a measurement run be read back from the events.
	RunID  string `json:"run_id,omitempty"`
	Region string `json:"region,omitempty"`
	// PolicyFlags are the content-rule kinds matched (never the content);
	// CacheSkip is the first reason the cache could not see the request,
	// empty when it was eligible. Together they make the policy filter's
	// decisions auditable per request.
	PolicyFlags []string `json:"policy_flags,omitempty"`
	CacheSkip   string   `json:"cache_skip,omitempty"`
	// ServedProvider names the backend that answered (after fallback);
	// Attempts counts backend attempts, so failover is observable.
	ServedProvider string `json:"served_provider,omitempty"`
	Attempts       int    `json:"attempts,omitempty"`
	Usage          Usage  `json:"usage,omitzero"`
	// TokensCharged is the admission charge (input estimate + output
	// reservation) so reservation error is measurable against Usage.
	TokensCharged int `json:"tokens_charged,omitempty"`
	// Cost is the request's price against the configured table; zero
	// (and omitted) when no table is configured or usage is unknown.
	Cost Cost `json:"cost,omitzero"`
	// SpendUSDMonth is the tenant's month-to-date spend for budgeted
	// tenants: the total after this request, or the total that denied it.
	SpendUSDMonth float64 `json:"spend_usd_month,omitempty"`
}

// Cost is a priced usage: dollars and the version of the table that
// produced them, so reports stay consistent when prices change.
type Cost struct {
	USD          float64 `json:"usd"`
	TableVersion string  `json:"table_version"`
}
