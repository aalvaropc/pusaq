package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	Stream    bool               `json:"stream"`
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

		if cr.Stream {
			return streamCompletion(ctx, p, req, cr, route)
		}

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

// streamCompletion proxies a streaming completion: the provider's raw SSE
// stream passes through untouched while a scanner watches for the usage
// chunk and records it on the envelope for accounting.
func streamCompletion(ctx context.Context, p provider.Provider, req *core.Request, cr completionRequest, route Route) (*core.Response, error) {
	upstream, err := p.StreamComplete(ctx, provider.ChatRequest{
		Model:     route.Model,
		Messages:  cr.Messages,
		MaxTokens: cr.MaxTokens,
	})
	if err != nil {
		return nil, err
	}

	resp := &core.Response{
		Status: http.StatusOK,
		Stream: &usageScanningStream{inner: upstream, req: req},
	}
	resp.SetHeader("Content-Type", "text/event-stream")
	resp.SetHeader("Cache-Control", "no-cache")
	resp.SetHeader(ServedModelHeader, route.Model)
	return resp, nil
}

// usageScanningStream forwards SSE bytes untouched while watching complete
// "data:" lines for a usage object; found usage lands on the envelope.
// Pusaq never rewrites provider chunks — passthrough fidelity first.
type usageScanningStream struct {
	inner io.ReadCloser
	req   *core.Request
	buf   []byte
}

func (u *usageScanningStream) Read(p []byte) (int, error) {
	n, err := u.inner.Read(p)
	if n > 0 {
		u.scan(p[:n])
	}
	return n, err
}

func (u *usageScanningStream) Close() error { return u.inner.Close() }

// scan accumulates bytes and inspects each complete line for a usage chunk.
func (u *usageScanningStream) scan(b []byte) {
	u.buf = append(u.buf, b...)
	for {
		i := bytes.IndexByte(u.buf, '\n')
		if i < 0 {
			return
		}
		line := bytes.TrimSpace(u.buf[:i])
		u.buf = u.buf[i+1:]

		payload, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		payload = bytes.TrimSpace(payload)
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var chunk struct {
			Usage *core.Usage `json:"usage"`
		}
		if err := json.Unmarshal(payload, &chunk); err == nil && chunk.Usage != nil {
			u.req.Usage = *chunk.Usage
		}
	}
}
