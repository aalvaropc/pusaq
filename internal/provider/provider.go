// Package provider defines Pusaq's upstream contract — the seam where model
// adapters plug in. The gateway talks to providers through the Provider
// interface with domain types; today the only adapter is the HTTP one used
// against the mock provider, tomorrow Vertex adapters implement the same
// interface.
package provider

import (
	"context"
	"io"

	"github.com/aalvaropc/pusaq/internal/core"
)

// Message is one turn of a chat conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the provider-agnostic completion request. Model carries the
// physical upstream model name resolved from the alias by configuration.
type ChatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	// Temperature is forwarded as the client sent it; a pointer so an
	// explicit 0 survives (omitted only when the client omitted it).
	Temperature *float64 `json:"temperature,omitempty"`
	Stream      bool     `json:"stream,omitempty"`
}

// Choice is one completion alternative.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ChatResponse is the provider-agnostic completion result.
type ChatResponse struct {
	ID      string     `json:"id"`
	Model   string     `json:"model"`
	Choices []Choice   `json:"choices"`
	Usage   core.Usage `json:"usage"`
}

// Provider completes chat requests against one upstream. Implementations
// return normalized errors: an upstream failure or timeout is
// core.CodeUpstream, never the provider's raw response.
type Provider interface {
	// Name identifies the provider in routing tables and logs.
	Name() string
	// Complete performs a non-streaming chat completion.
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// StreamComplete performs a streaming completion and returns the raw
	// SSE byte stream (OpenAI chunk shape, terminated by "data: [DONE]").
	// The caller owns the stream and must close it.
	StreamComplete(ctx context.Context, req ChatRequest) (io.ReadCloser, error)
}
