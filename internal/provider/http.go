package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

// maxUpstreamBody bounds how much of an upstream response is read.
const maxUpstreamBody = 4 << 20 // 4 MiB

// HTTP is a Provider adapter for any OpenAI-compatible chat-completions
// endpoint, the mock provider included.
type HTTP struct {
	name         string
	baseURL      string
	apiKey       string
	client       *http.Client
	streamClient *http.Client
	idleTimeout  time.Duration
	logger       *slog.Logger
}

// Option customizes an HTTP adapter.
type Option func(*HTTP)

// WithAPIKey authenticates every call to this upstream with a bearer key.
// The key is attached per request and never logged.
func WithAPIKey(key string) Option {
	return func(h *HTTP) { h.apiKey = key }
}

// NewHTTP builds an adapter for the endpoint at baseURL (no trailing slash).
// Buffered calls are bounded end to end by timeout; streamed calls bound by
// it the connection, the TLS handshake, the response headers, and every
// silence between chunks.
func NewHTTP(name, baseURL string, timeout time.Duration, logger *slog.Logger, opts ...Option) *HTTP {
	h := &HTTP{
		name:         name,
		baseURL:      baseURL,
		client:       &http.Client{Timeout: timeout},
		streamClient: &http.Client{Transport: streamTransport(timeout)},
		idleTimeout:  timeout,
		logger:       logger,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// authorize attaches the upstream's bearer key when configured.
func (p *HTTP) authorize(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
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
	p.authorize(httpReq)

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

	ctx, cancel := context.WithCancelCause(ctx)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		cancel(nil)
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	p.authorize(httpReq)

	httpResp, err := p.streamClient.Do(httpReq)
	if err != nil {
		cancel(nil)
		p.logger.Warn("upstream stream call failed",
			slog.String("provider", p.name), slog.Any("error", err))
		return nil, &core.Error{
			Code: core.CodeUpstream, Message: "upstream provider unavailable", Err: err,
		}
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 512))
		_ = httpResp.Body.Close()
		cancel(nil)
		p.logger.Warn("upstream stream error response",
			slog.String("provider", p.name),
			slog.Int("status", httpResp.StatusCode),
			slog.String("body", string(body)),
		)
		return nil, core.Errorf(core.CodeUpstream,
			"upstream provider returned status %d", httpResp.StatusCode)
	}
	return newIdleTimeoutBody(httpResp.Body, p.idleTimeout, cancel), nil
}

var errStreamIdle = errors.New("upstream stream idle past the timeout")

func streamTransport(timeout time.Duration) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = timeout
	t.ResponseHeaderTimeout = timeout
	return t
}

type idleTimeoutBody struct {
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
	cancel  context.CancelCauseFunc
	expired atomic.Bool
}

func newIdleTimeoutBody(body io.ReadCloser, timeout time.Duration, cancel context.CancelCauseFunc) *idleTimeoutBody {
	b := &idleTimeoutBody{body: body, timeout: timeout, cancel: cancel}
	b.timer = time.AfterFunc(timeout, func() {
		b.expired.Store(true)
		cancel(errStreamIdle)
	})
	return b
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if b.expired.Load() {
		return n, errStreamIdle
	}
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	err := b.body.Close()
	b.cancel(nil)
	return err
}
