package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

// maxUpstreamBody bounds how much of an upstream response is read.
const maxUpstreamBody = 4 << 20 // 4 MiB

// HTTP is a Provider adapter for any OpenAI-compatible chat-completions
// endpoint — the mock provider today, other compatible upstreams tomorrow.
type HTTP struct {
	name    string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// NewHTTP builds an adapter for the endpoint at baseURL (no trailing slash),
// with every call bounded by timeout.
func NewHTTP(name, baseURL string, timeout time.Duration, logger *slog.Logger) *HTTP {
	return &HTTP{
		name:    name,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

// Name implements Provider.
func (p *HTTP) Name() string { return p.name }

// Complete implements Provider. Upstream failures and timeouts map to
// core.CodeUpstream; the upstream's raw body is logged, never returned.
func (p *HTTP) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal upstream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		p.logger.Warn("upstream call failed",
			slog.String("provider", p.name), slog.Any("error", err))
		return ChatResponse{}, &core.Error{
			Code: core.CodeUpstream, Message: "upstream provider unavailable", Err: err,
		}
	}
	defer func() { _ = httpResp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, maxUpstreamBody))
	if err != nil {
		return ChatResponse{}, &core.Error{
			Code: core.CodeUpstream, Message: "failed reading upstream response", Err: err,
		}
	}

	if httpResp.StatusCode != http.StatusOK {
		p.logger.Warn("upstream error response",
			slog.String("provider", p.name),
			slog.Int("status", httpResp.StatusCode),
			slog.String("body", string(body[:min(len(body), 512)])),
		)
		return ChatResponse{}, core.Errorf(core.CodeUpstream,
			"upstream provider returned status %d", httpResp.StatusCode)
	}

	var resp ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ChatResponse{}, &core.Error{
			Code: core.CodeUpstream, Message: "invalid upstream response body", Err: err,
		}
	}
	return resp, nil
}
