// Package core holds Pusaq's pure domain types and the request pipeline
// contract. It depends on the standard library only and never imports other
// internal packages, so the domain stays free of transport and infrastructure
// concerns.
package core

import (
	"io"
	"time"
)

// Request is the transport-agnostic envelope that traverses the pipeline.
// It starts minimal on purpose: fields for tenant, model, and tokens arrive
// with the slices that use them.
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
	// ServedModel is the physical upstream model that handled the request.
	ServedModel string
	// Usage accumulates provider-reported token counts for accounting.
	Usage Usage
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
