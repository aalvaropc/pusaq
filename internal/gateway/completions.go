package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
	"github.com/aalvaropc/pusaq/internal/semcache"
)

// ServedModelHeader reports the physical model that served the request
// (constitution rule 7).
const ServedModelHeader = "X-Pusaq-Model"

// TokenLimiter is the TPM decision surface: charge on admission, reconcile
// once actual consumption is known. Satisfied by *ratelimit.Limiter; nil
// disables token limiting.
type TokenLimiter interface {
	Allow(ctx context.Context, tenant string, cost float64) (ratelimit.Decision, error)
	Adjust(ctx context.Context, tenant string, delta float64) error
}

// estimateInputTokens approximates prompt tokens as ceil(chars/4) — crude,
// but its error is measurable: tokens_charged and actual usage land side by
// side on every cost event. A real tokenizer is a later, earned dependency.
func estimateInputTokens(messages []provider.Message) int {
	chars := 0
	for _, m := range messages {
		chars += len(m.Role) + len(m.Content)
	}
	return (chars + 3) / 4
}

// Route maps a model alias to a provider and its physical upstream model,
// with ordered fallback backends serving the equivalent model. Routing
// tables are configuration: physical model names never appear in code
// (constitution rule 1).
type Route struct {
	Provider string
	Model    string
	// Fallbacks are tried in order, at most once each, when the primary
	// fails with an upstream error or its breaker is open.
	Fallbacks []string
}

// backends returns the primary followed by the fallbacks.
func (r Route) backends() []string {
	return append([]string{r.Provider}, r.Fallbacks...)
}

// BreakerFor returns the circuit breaker guarding a backend; nil disables
// breaking for it. Satisfied by a map lookup in the composition root.
type BreakerFor func(backend string) *breaker.Breaker

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

// ShadowCache receives samples of successful completions for shadow-mode
// evaluation. Satisfied by *semcache.Shadow; nil disables shadowing.
type ShadowCache interface {
	Offer(sample semcache.Sample)
}

// CompletionsOptions parameterize the completions flow.
type CompletionsOptions struct {
	// Tokens enables TPM limiting when non-nil.
	Tokens TokenLimiter
	// ReserveDefault is the output reservation when max_tokens is absent.
	ReserveDefault int
	// Breakers guards upstream backends; nil disables circuit breaking.
	Breakers BreakerFor
	// Shadow enables shadow-mode cache evaluation when non-nil.
	Shadow ShadowCache
	Logger *slog.Logger
}

// cacheable reports whether a request is structurally eligible for the
// semantic cache (§5.4): exactly one user message, optionally preceded by
// system messages. Multi-turn is never cacheable. It returns the system
// prompt (namespace material) and the user content (the embedded text).
func cacheable(messages []provider.Message) (systemPrompt, userContent string, ok bool) {
	users := 0
	var sys strings.Builder
	for _, m := range messages {
		switch m.Role {
		case "system":
			if users > 0 {
				return "", "", false // system after user: not the simple shape
			}
			sys.WriteString(m.Content)
			sys.WriteByte('\n')
		case "user":
			users++
			userContent = m.Content
		default:
			return "", "", false // assistant/tool turns: multi-turn
		}
	}
	if users != 1 {
		return "", "", false
	}
	return sys.String(), userContent, true
}

// offerShadow registers a finalizer that enqueues a shadow sample once the
// request has fully finished (buffered return or stream end) — the hot path
// does nothing but this registration. completed gates the offer: a stream
// that died mid-way must never be inserted as a cacheable observation.
func offerShadow(req *core.Request, cr completionRequest, opts CompletionsOptions, completed func() bool, fingerprint func() string) {
	if opts.Shadow == nil {
		return
	}
	systemPrompt, userContent, ok := cacheable(cr.Messages)
	if !ok {
		return
	}
	tenant, model, id := req.Tenant, req.ServedModel, req.ID
	req.OnFinalize(func() {
		if !completed() {
			return
		}
		opts.Shadow.Offer(semcache.Sample{
			RequestID:   id,
			Namespace:   semcache.Namespace(tenant, model, systemPrompt),
			Prompt:      userContent,
			Fingerprint: fingerprint(),
		})
	})
}

// always is the completed-gate for buffered successes.
func always() bool { return true }

// breakerFor resolves the breaker for a backend, tolerating a nil registry.
func (o CompletionsOptions) breakerFor(backend string) *breaker.Breaker {
	if o.Breakers == nil {
		return nil
	}
	return o.Breakers(backend)
}

// CompletionsHandler is the terminal handler for POST /v1/chat/completions:
// validate → resolve the alias from the routing table → charge the token
// budget (constitution rule 4: only after model resolution) → call the
// provider → map the response. Reconciliation of the token charge is
// registered as a finalizer, so it holds on success, error, panic, and at
// stream end.
func CompletionsHandler(routes map[string]Route, providers map[string]provider.Provider, opts CompletionsOptions) core.Handler {
	return func(ctx context.Context, req *core.Request) (*core.Response, error) {
		cr, err := parseCompletionRequest(req.Payload)
		if err != nil {
			return nil, err
		}

		route, ok := routes[cr.Model]
		if !ok {
			return nil, core.Errorf(core.CodeInvalidRequest, "unknown model %q", cr.Model)
		}
		req.RequestedModel = cr.Model
		req.ServedModel = route.Model

		// TPM is charged once, before any backend attempt: the model
		// contract is identical across the fallback chain.
		if err := chargeTokens(ctx, req, cr, opts); err != nil {
			return nil, err
		}

		if cr.Stream {
			return streamCompletion(ctx, providers, req, cr, route, opts)
		}

		upstream, err := completeWithFallback(ctx, providers, req, cr, route, opts)
		if err != nil {
			return nil, err
		}
		req.Usage = upstream.Usage

		var content strings.Builder
		for _, c := range upstream.Choices {
			content.WriteString(c.Message.Content)
		}
		offerShadow(req, cr, opts, always, func() string { return semcache.Fingerprint(content.String()) })

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

// chargeTokens charges the tenant's token bucket with the request's input
// estimate plus output reservation, and registers the exactly-once
// reconciliation finalizer. A nil limiter disables TPM.
func chargeTokens(ctx context.Context, req *core.Request, cr completionRequest, opts CompletionsOptions) error {
	if opts.Tokens == nil {
		return nil
	}
	reserve := opts.ReserveDefault
	if cr.MaxTokens > 0 {
		reserve = cr.MaxTokens
	}
	charged := estimateInputTokens(cr.Messages) + reserve

	limCtx, cancel := context.WithTimeout(ctx, limiterBudget)
	d, err := opts.Tokens.Allow(limCtx, req.Tenant, float64(charged))
	cancel()
	if err != nil {
		opts.Logger.Warn("token limiter store unavailable, failing open",
			slog.String("request_id", req.ID),
			slog.String("tenant", req.Tenant),
			slog.Any("error", err),
		)
		return nil
	}
	if !d.Allowed {
		return &core.Error{
			Code:       core.CodeRateLimited,
			Message:    "token rate limit exceeded",
			RetryAfter: d.RetryAfter,
		}
	}
	req.TokensCharged = charged

	req.OnFinalize(func() {
		// Actual usage unknown → the full charge stands (refunding on
		// unknown actuals would let clients zero charges by aborting).
		if req.Usage == (core.Usage{}) {
			return
		}
		actual := req.Usage.PromptTokens + req.Usage.CompletionTokens
		delta := float64(charged - actual) // >0 refund, <0 extra charge
		if delta == 0 {
			return
		}
		adjCtx, cancel := context.WithTimeout(context.Background(), limiterBudget)
		defer cancel()
		if err := opts.Tokens.Adjust(adjCtx, req.Tenant, delta); err != nil {
			opts.Logger.Warn("token reconciliation failed; charge stands until bucket TTL",
				slog.String("request_id", req.ID),
				slog.String("tenant", req.Tenant),
				slog.Any("error", err),
			)
		}
	})
	return nil
}

// completeWithFallback walks the route's backend chain: skip open breakers
// without paying an attempt, feed every outcome back to the breaker, serve
// from the first success. Non-upstream errors stop the chain — they are not
// the backend's fault.
func completeWithFallback(ctx context.Context, providers map[string]provider.Provider, req *core.Request, cr completionRequest, route Route, opts CompletionsOptions) (provider.ChatResponse, error) {
	chatReq := provider.ChatRequest{
		Model:     route.Model,
		Messages:  cr.Messages,
		MaxTokens: cr.MaxTokens,
	}
	var lastErr error
	for _, backend := range route.backends() {
		p, ok := providers[backend]
		if !ok {
			return provider.ChatResponse{}, core.Errorf(core.CodeInternal, "no provider registered for route")
		}
		br := opts.breakerFor(backend)
		if br != nil && !br.Allow() {
			opts.Logger.Warn("backend breaker open, skipping",
				slog.String("request_id", req.ID), slog.String("backend", backend))
			continue
		}
		req.Attempts++
		resp, err := p.Complete(ctx, chatReq)
		if br != nil {
			br.Record(err == nil || core.AsError(err).Code != core.CodeUpstream)
		}
		if err == nil {
			req.ServedProvider = backend
			return resp, nil
		}
		if core.AsError(err).Code != core.CodeUpstream {
			return provider.ChatResponse{}, err
		}
		lastErr = err
		opts.Logger.Warn("backend failed, trying next",
			slog.String("request_id", req.ID), slog.String("backend", backend), slog.Any("error", err))
	}
	if lastErr != nil {
		return provider.ChatResponse{}, lastErr
	}
	return provider.ChatResponse{}, core.NewError(core.CodeUpstream, "all upstream backends unavailable")
}

// streamCompletion proxies a streaming completion with pre-first-byte
// fallback: backend failures before a stream exists walk the chain; once a
// stream has begun, mid-stream failures end it with the usual accounting —
// a started stream is never silently restarted.
func streamCompletion(ctx context.Context, providers map[string]provider.Provider, req *core.Request, cr completionRequest, route Route, opts CompletionsOptions) (*core.Response, error) {
	chatReq := provider.ChatRequest{
		Model:     route.Model,
		Messages:  cr.Messages,
		MaxTokens: cr.MaxTokens,
	}
	var upstream io.ReadCloser
	var lastErr error
	for _, backend := range route.backends() {
		p, ok := providers[backend]
		if !ok {
			return nil, core.Errorf(core.CodeInternal, "no provider registered for route")
		}
		br := opts.breakerFor(backend)
		if br != nil && !br.Allow() {
			opts.Logger.Warn("backend breaker open, skipping",
				slog.String("request_id", req.ID), slog.String("backend", backend))
			continue
		}
		req.Attempts++
		stream, err := p.StreamComplete(ctx, chatReq)
		if br != nil {
			br.Record(err == nil || core.AsError(err).Code != core.CodeUpstream)
		}
		if err == nil {
			req.ServedProvider = backend
			upstream = stream
			break
		}
		if core.AsError(err).Code != core.CodeUpstream {
			return nil, err
		}
		lastErr = err
		opts.Logger.Warn("backend failed, trying next",
			slog.String("request_id", req.ID), slog.String("backend", backend), slog.Any("error", err))
	}
	if upstream == nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, core.NewError(core.CodeUpstream, "all upstream backends unavailable")
	}

	scanner := &usageScanningStream{inner: upstream, req: req}
	offerShadow(req, cr, opts, scanner.completed, scanner.contentFingerprint)

	resp := &core.Response{
		Status: http.StatusOK,
		Stream: scanner,
	}
	resp.SetHeader("Content-Type", "text/event-stream")
	resp.SetHeader("Cache-Control", "no-cache")
	resp.SetHeader(ServedModelHeader, route.Model)
	return resp, nil
}

// usageScanningStream forwards SSE bytes untouched while watching complete
// "data:" lines for a usage object (landed on the envelope) and
// accumulating delta content for the response fingerprint. Pusaq never
// rewrites provider chunks — passthrough fidelity first.
type usageScanningStream struct {
	inner   io.ReadCloser
	req     *core.Request
	buf     []byte
	content strings.Builder
	done    bool
}

// completed reports whether the stream reached its [DONE] terminator — the
// gate that keeps aborted streams out of the shadow cache.
func (u *usageScanningStream) completed() bool { return u.done }

// contentFingerprint hashes the content the scanner has seen; called at
// finalize time, after the stream ended.
func (u *usageScanningStream) contentFingerprint() string {
	return semcache.Fingerprint(u.content.String())
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
		if len(payload) == 0 {
			continue
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			u.done = true
			continue
		}
		var chunk struct {
			Usage   *core.Usage `json:"usage"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(payload, &chunk); err == nil {
			if chunk.Usage != nil {
				u.req.Usage = *chunk.Usage
			}
			for _, c := range chunk.Choices {
				u.content.WriteString(c.Delta.Content)
			}
		}
	}
}
