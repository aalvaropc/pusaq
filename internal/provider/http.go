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
	// client bounds buffered calls end to end.
	client *http.Client
	// streamClient bounds only time-to-first-byte: a whole-exchange
	// timeout would kill any generation longer than the limit.
	streamClient *http.Client
	logger       *slog.Logger
}

// NewHTTP builds an adapter for the endpoint at baseURL (no trailing slash).
// Buffered calls are bounded end to end by timeout; streamed calls bound
// only the response headers by it.
func NewHTTP(name, baseURL string, timeout time.Duration, logger *slog.Logger) *HTTP {
	return &HTTP{
		name:    name,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		streamClient: &http.Client{
			Transport: &http.Transport{ResponseHeaderTimeout: timeout},
		},
		logger: logger,
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

// StreamComplete implements Provider. On success the returned stream is the
// upstream's raw SSE body; the caller closes it. Failures before the stream
// starts map to core.CodeUpstream like buffered calls.
func (p *HTTP) StreamComplete(ctx context.Context, req ChatRequest) (io.ReadCloser, error) {
	req.Stream = true
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := p.streamClient.Do(httpReq)
	if err != nil {
		p.logger.Warn("upstream stream call failed",
			slog.String("provider", p.name), slog.Any("error", err))
		return nil, &core.Error{
			Code: core.CodeUpstream, Message: "upstream provider unavailable", Err: err,
		}
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 512))
		_ = httpResp.Body.Close()
		p.logger.Warn("upstream stream error response",
			slog.String("provider", p.name),
			slog.Int("status", httpResp.StatusCode),
			slog.String("body", string(body)),
		)
		return nil, core.Errorf(core.CodeUpstream,
			"upstream provider returned status %d", httpResp.StatusCode)
	}
	return httpResp.Body, nil
}
