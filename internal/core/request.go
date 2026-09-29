// Package core holds Pusaq's pure domain types and the request pipeline
// contract. It depends on the standard library only and never imports other
// internal packages, so the domain stays free of transport and infrastructure
// concerns.
package core

import (
	"io"
	"time"
)

// DevTenant is the tenant every request is attributed to when no API keys
// are configured (dev mode — explicit, never silent).
const DevTenant = "dev"

// Request is the transport-agnostic envelope that traverses the pipeline.
// Only the fields the pipeline stages actually use live here.
type Request struct {
	// ID uniquely identifies the request. Assigned by the request-ID stage;
	// empty until then.
	ID string

	// ArrivedAt is the instant the gateway admitted the request.
	ArrivedAt time.Time

	// Op names the gateway operation being performed
	// (e.g. "chat.completions"). It labels cost events and routing.
	Op string

	// APIKey is the bearer token the transport extracted; consumed by the
	// auth stage and never logged.
	APIKey string
	// Tenant is who this request is attributed to, set by the auth stage.
	Tenant string

	// Payload carries the operation-specific request body.
	Payload []byte

	// RequestedModel is the model alias the client asked for (e.g.
	// "pusaq/fast"); set by operations that resolve models.
	RequestedModel string
	// FeatureTag is the request's declared feature (metadata.feature_tag),
	// empty when absent; RouteReason says how the alias was resolved.
	FeatureTag  string
	RouteReason string
	// RunID is the experiment run the client declared (metadata.run_id),
	// empty when absent; it tags the cost event so a run is auditable from
	// the log alone.
	RunID string
	// ServedModel is the physical upstream model that handled the request.
	ServedModel string
	// ServedProvider is the upstream backend that actually answered
	// (after any fallback); Attempts counts backend attempts made.
	ServedProvider string
	Attempts       int
	// Usage accumulates provider-reported token counts for accounting.
	Usage Usage
	// TokensCharged is what the token limiter charged on admission
	// (input estimate + output reservation); zero when TPM is disabled.
	TokensCharged int
	// Cost is set by a finalizer once usage is known and a price table is
	// configured.
	Cost Cost
	// SpendUSDMonth is the tenant's month-to-date spend after this request
	// (or the total that caused a budget denial); zero when unbudgeted.
	SpendUSDMonth float64

	// finalizers run exactly once when the request is finalized by the
	// accounting stage — buffered return, stream end, or panic — before
	// the cost event is emitted. Registered via OnFinalize.
	finalizers []func()
}

// OnFinalize registers fn to run exactly once when the request finalizes
// (before its cost event is emitted), whatever the outcome. Used for
// obligations that must hold on every path, like token reconciliation.
func (r *Request) OnFinalize(fn func()) {
	r.finalizers = append(r.finalizers, fn)
}

// RunFinalizers executes registered finalizers in order. Called by the
// accounting stage only.
func (r *Request) RunFinalizers() {
	for _, fn := range r.finalizers {
		fn()
	}
	r.finalizers = nil
}

// Usage is the provider-reported token consumption of a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Response is the transport-agnostic result of a pipeline run.
type Response struct {
	// Status is the HTTP status the transport layer should render.
	Status int

	// Header carries response metadata (e.g. the request ID header).
	Header map[string]string

	// Body is the operation-specific response body, already serialized.
	// Ignored when Stream is non-nil.
	Body []byte

	// Stream, when non-nil, is a response delivered incrementally (SSE).
	// The transport layer copies it to the client with per-read flushes
	// and closes it; the accounting stage finalizes the cost event when
	// the stream ends.
	Stream io.ReadCloser
}

// SetHeader records a response header, allocating the map on first use.
func (r *Response) SetHeader(key, value string) {
	if r.Header == nil {
		r.Header = make(map[string]string, 1)
	}
	r.Header[key] = value
}
