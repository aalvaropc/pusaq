package gateway

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

// ServedModelHeader reports the physical model that served the request
// (constitution rule 7).
const ServedModelHeader = "X-Pusaq-Model"

// Route maps a model alias to a provider and its physical upstream model.
// Routing tables are configuration: physical model names never appear in
// code (constitution rule 1).
type Route struct {
	Provider string
	Model    string
}

// completionRequest is the accepted OpenAI request subset. Unknown fields
// are ignored for SDK compatibility.
type completionRequest struct {
	Model     string             `json:"model"`
	Messages  []provider.Message `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
}

// completionResponse is the OpenAI response subset Pusaq returns.
type completionResponse struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Model   string            `json:"model"`
	Choices []provider.Choice `json:"choices"`
	Usage   usagePayload      `json:"usage"`
}

// usagePayload renders usage with the total the OpenAI shape includes.
type usagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// parseCompletionRequest validates the request subset: valid JSON, a model,
// and at least one message.
func parseCompletionRequest(payload []byte) (completionRequest, error) {
	var req completionRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return req, core.NewError(core.CodeInvalidRequest, "request body is not valid JSON")
	}
	if req.Model == "" {
		return req, core.NewError(core.CodeInvalidRequest, "model is required")
	}
	if len(req.Messages) == 0 {
		return req, core.NewError(core.CodeInvalidRequest, "messages must not be empty")
	}
	return req, nil
}

// CompletionsHandler is the terminal handler for POST /v1/chat/completions:
// validate → resolve the alias from the routing table → call the provider →
// map the response. It records requested/served model and usage on the
// envelope so the accounting stage picks them up.
func CompletionsHandler(routes map[string]Route, providers map[string]provider.Provider) core.Handler {
	return func(ctx context.Context, req *core.Request) (*core.Response, error) {
		cr, err := parseCompletionRequest(req.Payload)
		if err != nil {
			return nil, err
		}

		route, ok := routes[cr.Model]
		if !ok {
			return nil, core.Errorf(core.CodeInvalidRequest, "unknown model %q", cr.Model)
		}
		p, ok := providers[route.Provider]
		if !ok {
			// A route naming an unregistered provider is a config bug.
			return nil, core.Errorf(core.CodeInternal, "no provider registered for route")
		}
		req.RequestedModel = cr.Model
		req.ServedModel = route.Model

		upstream, err := p.Complete(ctx, provider.ChatRequest{
			Model:     route.Model,
			Messages:  cr.Messages,
			MaxTokens: cr.MaxTokens,
		})
		if err != nil {
			return nil, err
		}
		req.Usage = upstream.Usage

		body, err := json.Marshal(completionResponse{
			ID:      upstream.ID,
			Object:  "chat.completion",
			Model:   upstream.Model,
			Choices: upstream.Choices,
			Usage: usagePayload{
				PromptTokens:     upstream.Usage.PromptTokens,
				CompletionTokens: upstream.Usage.CompletionTokens,
				TotalTokens:      upstream.Usage.PromptTokens + upstream.Usage.CompletionTokens,
			},
		})
		if err != nil {
			return nil, core.NewError(core.CodeInternal, "failed to encode response")
		}

		resp := &core.Response{Status: http.StatusOK, Body: body}
		resp.SetHeader("Content-Type", "application/json")
		resp.SetHeader(ServedModelHeader, route.Model)
		return resp, nil
	}
}
