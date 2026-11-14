// Package provider defines Pusaq's upstream contract — the seam where model
// adapters plug in. The gateway talks to providers through the Provider
// interface with domain types; the HTTP adapter serves every
// OpenAI-compatible upstream, the mock provider included.
package provider

import (
	"context"
	"encoding/json"
	"io"

	"github.com/aalvaropc/pusaq/internal/core"
)

// Message is one turn of a chat conversation.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// MarshalJSON renders a tool-call message without text with content null.
func (m Message) MarshalJSON() ([]byte, error) {
	content := &m.Content
	if m.Content == "" && len(m.ToolCalls) > 0 {
		content = nil
	}
	return json.Marshal(struct {
		Role       string     `json:"role"`
		Content    *string    `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}{m.Role, content, m.ToolCalls, m.ToolCallID})
}

// ToolCall is one function call made by an assistant message.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names the called function and carries its arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function the model may call.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function declares a tool's name, description and JSON Schema parameters.
type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolChoiceFunction is the ToolChoice mode that forces one function.
const ToolChoiceFunction = "function"

// ToolChoice is none, auto, required, or ToolChoiceFunction with a name.
type ToolChoice struct {
	Mode     string
	Function string
}

// MarshalJSON renders the OpenAI string or function object.
func (c ToolChoice) MarshalJSON() ([]byte, error) {
	if c.Mode != ToolChoiceFunction {
		return json.Marshal(c.Mode)
	}
	type name struct {
		Name string `json:"name"`
	}
	return json.Marshal(struct {
		Type     string `json:"type"`
		Function name   `json:"function"`
	}{ToolChoiceFunction, name{c.Function}})
}

// ChatRequest is the provider-agnostic completion request. Model carries the
// physical upstream model name resolved from the alias by configuration.
type ChatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	// Temperature is forwarded as the client sent it; a pointer so an
	// explicit 0 survives (omitted only when the client omitted it).
	Temperature       *float64    `json:"temperature,omitempty"`
	Tools             []Tool      `json:"tools,omitempty"`
	ToolChoice        *ToolChoice `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"`
	IdempotencyKey    string      `json:"-"`
}

// Choice is one completion alternative.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ChatResponse is the provider-agnostic completion result.
type ChatResponse struct {
	ID                string     `json:"id"`
	Created           int64      `json:"created"`
	Model             string     `json:"model"`
	SystemFingerprint string     `json:"system_fingerprint"`
	Choices           []Choice   `json:"choices"`
	Usage             core.Usage `json:"usage"`
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
