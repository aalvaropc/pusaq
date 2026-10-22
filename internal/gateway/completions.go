package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/pricing"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
	"github.com/aalvaropc/pusaq/internal/semcache"
)

// ServedModelHeader reports the physical model that served the request
// (constitution rule 7).
const ServedModelHeader = "X-Pusaq-Model"

// RouteReasonHeader reports how the requested alias was resolved: "direct"
// for a concrete alias; for pusaq/auto, "rule:<tag>" when a routing rule
// named the request's feature tag, "default:untagged" when it carried none,
// "default:unknown_tag" when no rule named it.
const RouteReasonHeader = "X-Pusaq-Route-Reason"

// AutoAlias is the router alias; it resolves through RoutingRules.
const AutoAlias = "pusaq/auto"

// RoutingRules is the explicit routing table for AutoAlias (docs/design.md
// §5.5): feature tag → concrete alias, with a default for the rest.
type RoutingRules struct {
	ByTag   map[string]string
	Default string
}

// resolve maps a requested alias (and the request's feature tag) to the
// concrete alias to serve and the reason to report.
func (r RoutingRules) resolve(requested, tag string) (alias, reason string) {
	if requested != AutoAlias {
		return requested, "direct"
	}
	if tag == "" {
		return r.Default, "default:untagged"
	}
	if a, ok := r.ByTag[tag]; ok {
		return a, "rule:" + tag
	}
	return r.Default, "default:unknown_tag"
}

// TokenLimiter is the TPM decision surface: charge on admission, reconcile
// once actual consumption is known. Satisfied by *ratelimit.Limiter; nil
// disables token limiting.
type TokenLimiter interface {
	Allow(ctx context.Context, tenant string, cost float64) (ratelimit.Decision, error)
	Adjust(ctx context.Context, tenant string, delta float64) error
	Limits() ratelimit.Limits
}

// estimateInputTokens approximates prompt tokens as ceil(chars/4) — crude,
// but its error is measurable: tokens_charged and actual usage land side by
// side on every cost event.
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

// completionRequest is the accepted OpenAI request subset.
type completionRequest struct {
	Model    string             `json:"model"`
	Messages []provider.Message `json:"messages"`
	// MaxTokens and Temperature are pointers so an explicit value is told
	// apart from an omitted one (the provider default).
	MaxTokens   *int     `json:"max_tokens"`
	Temperature *float64 `json:"temperature"`
	Stream      bool     `json:"stream"`
	// Metadata is the OpenAI metadata map; feature_tag drives the router.
	// run_id is read by the transport layer before admission (see
	// runIDFromPayload) so every event of the request carries it.
	Metadata map[string]any `json:"metadata"`
}

// featureTag extracts metadata.feature_tag: absent means untagged, a
// non-string, oversized or non-label value is a client error.
func featureTag(cr completionRequest) (string, error) {
	v, ok := cr.Metadata["feature_tag"]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", core.NewError(core.CodeInvalidRequest, "metadata.feature_tag must be a string")
	}
	if s != "" && (len(s) > maxLabelLen || !labelChars.MatchString(s)) {
		return "", core.Errorf(core.CodeInvalidRequest,
			"metadata.feature_tag must be at most %d letters, digits, '.', '_' or '-'", maxLabelLen)
	}
	return s, nil
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

// parseCompletionRequest validates the request subset.
func parseCompletionRequest(payload []byte) (completionRequest, error) {
	var req completionRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return req, decodeError(err)
	}
	if err := rejectUnsupported(payload); err != nil {
		return req, err
	}
	if req.Model == "" {
		return req, core.NewError(core.CodeInvalidRequest, "model is required")
	}
	if len(req.Messages) == 0 {
		return req, core.NewError(core.CodeInvalidRequest, "messages must not be empty")
	}
	if req.MaxTokens != nil && *req.MaxTokens < 1 {
		return req, errMaxTokens
	}
	return req, nil
}

var errMaxTokens = core.NewError(core.CodeInvalidRequest, "max_tokens must be a positive integer")

func decodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		return core.NewError(core.CodeInvalidRequest, "request body is not valid JSON")
	}
	switch typeErr.Field {
	case "max_tokens":
		return errMaxTokens
	case "messages.content":
		return core.NewError(core.CodeInvalidRequest, "message content must be a string; content parts are not supported")
	}
	return core.Errorf(core.CodeInvalidRequest, "%s has the wrong type", typeErr.Field)
}

var (
	unsupportedFields = []string{
		"tools", "tool_choice", "functions", "function_call", "parallel_tool_calls", "response_format",
		"n", "logprobs", "top_logprobs", "audio", "modalities", "prediction", "web_search_options",
		"max_completion_tokens",
	}
	unsupportedMessageFields = []string{"tool_calls", "tool_call_id", "function_call", "audio"}
	harmlessValues           = map[string]string{"n": "1", "modalities": `["text"]`}
)

func rejectUnsupported(payload []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(payload, &top); err != nil {
		return core.NewError(core.CodeInvalidRequest, "request body is not valid JSON")
	}
	for _, f := range unsupportedFields {
		if set(top[f], harmlessValues[f]) {
			return core.Errorf(core.CodeInvalidRequest, "%s is not supported", f)
		}
	}
	var messages []map[string]json.RawMessage
	_ = json.Unmarshal(top["messages"], &messages)
	for _, m := range messages {
		for _, f := range unsupportedMessageFields {
			if set(m[f], "") {
				return core.Errorf(core.CodeInvalidRequest, "messages[].%s is not supported", f)
			}
		}
	}
	return nil
}

func set(raw json.RawMessage, harmless string) bool {
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return len(raw) > 0
	}
	v := compact.String()
	return v != "" && v != "null" && v != "false" && v != harmless
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
	// Prices prices completions when non-nil; nil leaves cost events
	// without cost fields.
	Prices *pricing.Table
	// Rules resolve AutoAlias; a zero value makes auto requests fail as
	// unknown models.
	Rules RoutingRules
	// Policies resolves per-tenant policies; nil means no policies.
	Policies PolicyFor
	// Spend tracks monthly spend for budgeted tenants; nil disables budgets.
	Spend  SpendCounter
	Logger *slog.Logger
}

// priceOnFinalize registers the finalizer that prices the request after a
// buffered return or at stream end. A provider that answered without usage
// is priced from the reservation; a request no provider answered has no
// cost. The price is keyed by the served model, which the fallback chain
// preserves.
func priceOnFinalize(req *core.Request, cr completionRequest, opts CompletionsOptions) {
	if opts.Prices == nil {
		return
	}
	req.OnFinalize(func() {
		usage, estimated := req.Usage, false
		if usage == (core.Usage{}) {
			if req.ServedProvider == "" {
				return
			}
			usage = core.Usage{
				PromptTokens:     estimateInputTokens(cr.Messages),
				CompletionTokens: reservation(cr, opts.ReserveDefault),
			}
			estimated = true
		}
		if usd, ok := opts.Prices.Cost(req.ServedModel, usage); ok {
			req.Cost = core.Cost{USD: usd, TableVersion: opts.Prices.Version, Estimated: estimated}
		}
	})
}

func reservation(cr completionRequest, reserveDefault int) int {
	if cr.MaxTokens != nil {
		return *cr.MaxTokens
	}
	return reserveDefault
}

// offerShadow registers a finalizer that enqueues a shadow sample once the
// request has fully finished (buffered return or stream end) — the hot path
// does nothing but this registration. completed gates the offer: a stream
// that died mid-way must never be inserted as a cacheable observation.
func offerShadow(req *core.Request, shape cacheShape, opts CompletionsOptions, completed func() bool, fingerprint func() string) {
	if opts.Shadow == nil || req.CacheSkip != "" {
		return
	}
	tenant, model, id := req.Tenant, req.ServedModel, req.ID
	req.OnFinalize(func() {
		if !completed() {
			return
		}
		opts.Shadow.Offer(semcache.Sample{
			RequestID:   id,
			Namespace:   semcache.Namespace(tenant, model, shape.SystemPrompt, shape.Temperature),
			Prompt:      shape.UserContent,
			Fingerprint: fingerprint(),
		})
	})
}

// upstreamRequest is the one place the client's request becomes the
// provider's, so the buffered and streaming paths cannot drift apart.
func upstreamRequest(cr completionRequest, route Route) provider.ChatRequest {
	var maxTokens int
	if cr.MaxTokens != nil {
		maxTokens = *cr.MaxTokens
	}
	return provider.ChatRequest{
		Model:       route.Model,
		Messages:    cr.Messages,
		MaxTokens:   maxTokens,
		Temperature: cr.Temperature,
	}
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
		tag, err := featureTag(cr)
		if err != nil {
			return nil, err
		}
		alias, reason := opts.Rules.resolve(cr.Model, tag)
		route, ok := routes[alias]
		if !ok {
			return nil, core.Errorf(core.CodeInvalidRequest, "unknown model %q", cr.Model)
		}
		req.RequestedModel = cr.Model
		req.ServedModel = route.Model
		req.FeatureTag = tag
		req.RouteReason = reason
		priceOnFinalize(req, cr, opts)

		// Policy runs after the alias is known and before anything is
		// charged: a denied request costs the tenant nothing. Its content
		// flags then decide, once, whether the cache may see the request.
		flags, err := applyPolicy(ctx, req, cr, alias, opts)
		if err != nil {
			return nil, err
		}
		shape, skip := cacheEligibility(cr, opts.policyFor(req.Tenant), flags)
		req.CacheSkip = skip

		// TPM is charged once, before any backend attempt: the model
		// contract is identical across the fallback chain.
		if err := chargeTokens(ctx, req, cr, opts); err != nil {
			return nil, err
		}

		if cr.Stream {
			return streamCompletion(ctx, providers, req, cr, shape, route, opts)
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
		offerShadow(req, shape, opts, always, func() string { return semcache.Fingerprint(content.String()) })

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
		resp.SetHeader(RouteReasonHeader, req.RouteReason)
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
	reserve := reservation(cr, opts.ReserveDefault)
	estimate := estimateInputTokens(cr.Messages)
	if need, burst := float64(estimate)+float64(reserve), opts.Tokens.Limits().Burst; need > burst {
		return core.Errorf(core.CodeInvalidRequest,
			"request needs %.0f tokens (prompt estimate plus max_tokens), more than the token burst of %.0f", need, burst)
	}
	charged := estimate + reserve

	limCtx, cancel := context.WithTimeout(ctx, limiterBudget)
	d, err := opts.Tokens.Allow(limCtx, req.Tenant, float64(charged))
	cancel()
	if err != nil {
		if opts.policyFor(req.Tenant).FailClosed {
			opts.Logger.Warn("token limiter store unavailable, failing closed",
				slog.String("request_id", req.ID),
				slog.String("tenant", req.Tenant),
				slog.String("error_code", string(core.AsError(err).Code)),
			)
			return &core.Error{Code: core.CodeStoreUnavailable, Message: "token limiter store unavailable", Err: err}
		}
		opts.Logger.Warn("token limiter store unavailable, failing open",
			slog.String("request_id", req.ID),
			slog.String("tenant", req.Tenant),
			slog.String("error_code", string(core.AsError(err).Code)),
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
		if req.Usage == (core.Usage{}) && req.Attempts > 0 {
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
				slog.String("error_code", string(core.AsError(err).Code)),
			)
		}
	})
	return nil
}

// viaChain walks the route's backend chain: skip open breakers without
// paying an attempt, feed every failed attempt back to the breaker, and
// return the first success with its backend's breaker, on which the caller
// records the outcome. A client cancellation stops the chain and feeds
// nothing; non-upstream errors stop the chain — they are not the backend's
// fault.
func viaChain[T any](ctx context.Context, providers map[string]provider.Provider, req *core.Request, route Route, opts CompletionsOptions, call func(provider.Provider) (T, error)) (T, *breaker.Breaker, error) {
	var zero T
	var lastErr error
	for _, backend := range route.backends() {
		if ctx.Err() != nil {
			return zero, nil, errClientCanceled()
		}
		p, ok := providers[backend]
		if !ok {
			return zero, nil, core.Errorf(core.CodeInternal, "no provider registered for route")
		}
		br := opts.breakerFor(backend)
		if br != nil && !br.Allow() {
			opts.Logger.Warn("backend breaker open, skipping",
				slog.String("request_id", req.ID), slog.String("backend", backend))
			continue
		}
		req.Attempts++
		out, err := call(p)
		if err != nil && ctx.Err() != nil {
			if br != nil {
				br.Release()
			}
			return zero, nil, errClientCanceled()
		}
		if err == nil {
			req.ServedProvider = backend
			return out, br, nil
		}
		upstreamFailure := core.AsError(err).Code == core.CodeUpstream
		if br != nil {
			br.Record(!upstreamFailure)
		}
		if !upstreamFailure {
			return zero, nil, err
		}
		lastErr = err
		opts.Logger.Warn("backend failed, trying next",
			slog.String("request_id", req.ID), slog.String("backend", backend), slog.String("error_code", string(core.AsError(err).Code)))
	}
	if lastErr != nil {
		return zero, nil, lastErr
	}
	return zero, nil, core.NewError(core.CodeUpstream, "all upstream backends unavailable")
}

func errClientCanceled() error {
	return core.NewError(core.CodeClientCanceled, "client canceled the request")
}

func completeWithFallback(ctx context.Context, providers map[string]provider.Provider, req *core.Request, cr completionRequest, route Route, opts CompletionsOptions) (provider.ChatResponse, error) {
	chatReq := upstreamRequest(cr, route)
	out, br, err := viaChain(ctx, providers, req, route, opts, func(p provider.Provider) (provider.ChatResponse, error) {
		return p.Complete(ctx, chatReq)
	})
	if br != nil {
		br.Record(true)
	}
	return out, err
}

// streamCompletion proxies a streaming completion with pre-first-byte
// fallback: backend failures before a stream exists walk the chain; a
// started stream is never restarted, and its outcome reaches the backend's
// breaker when it ends.
func streamCompletion(ctx context.Context, providers map[string]provider.Provider, req *core.Request, cr completionRequest, shape cacheShape, route Route, opts CompletionsOptions) (*core.Response, error) {
	chatReq := upstreamRequest(cr, route)
	upstream, br, err := viaChain(ctx, providers, req, route, opts, func(p provider.Provider) (io.ReadCloser, error) {
		return p.StreamComplete(ctx, chatReq)
	})
	if err != nil {
		return nil, err
	}

	scanner := &usageScanningStream{inner: upstream, req: req}
	req.OnFinalize(func() { scanner.settle(br) })
	offerShadow(req, shape, opts, scanner.completed, scanner.contentFingerprint)

	resp := &core.Response{
		Status: http.StatusOK,
		Stream: scanner,
	}
	resp.SetHeader("Content-Type", "text/event-stream")
	resp.SetHeader("Cache-Control", "no-cache")
	resp.SetHeader(ServedModelHeader, route.Model)
	resp.SetHeader(RouteReasonHeader, req.RouteReason)
	return resp, nil
}

const maxEventBytes = 1 << 20

var errEventTooLarge = errors.New("upstream stream event exceeds 1 MiB")

const streamErrorEvent = "\n\ndata: " + `{"error":{"code":"upstream_error","message":"upstream stream failed"}}` + "\n\n"

type usageScanningStream struct {
	inner      io.ReadCloser
	req        *core.Request
	reader     *bufio.Reader
	event      []byte
	data       []byte
	hasData    bool
	eventError bool
	digest     hash.Hash
	ready      []byte
	done       bool
	failed     bool
	err        error
}

func (u *usageScanningStream) completed() bool { return u.done }

func (u *usageScanningStream) contentFingerprint() string {
	if u.digest == nil {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:8])
	}
	return hex.EncodeToString(u.digest.Sum(nil)[:8])
}

func (u *usageScanningStream) settle(br *breaker.Breaker) {
	switch {
	case br == nil:
	case u.failed:
		br.Record(false)
	case u.done:
		br.Record(true)
	default:
		br.Release()
	}
}

func (u *usageScanningStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if u.reader == nil {
		u.reader = bufio.NewReaderSize(u.inner, 4096)
	}
	for len(u.ready) == 0 && u.err == nil {
		u.readEvent()
	}
	if len(u.ready) > 0 {
		n := copy(p, u.ready)
		u.ready = u.ready[n:]
		return n, nil
	}
	return 0, u.err
}

func (u *usageScanningStream) Close() error { return u.inner.Close() }

func (u *usageScanningStream) readEvent() {
	u.event, u.data = u.event[:0], u.data[:0]
	u.hasData, u.eventError = false, false
	for {
		line, terminated, err := u.readLine()
		if err != nil && !errors.Is(err, io.EOF) {
			u.end(err)
			return
		}
		if len(line) > 0 {
			u.consume(line)
		}
		if (terminated && len(line) == 0) || errors.Is(err, io.EOF) {
			if dispatchErr := u.dispatch(); dispatchErr != nil {
				u.end(dispatchErr)
				return
			}
			u.ready = u.event
			if u.done || errors.Is(err, io.EOF) {
				u.end(io.EOF)
			}
			return
		}
	}
}

func (u *usageScanningStream) readLine() ([]byte, bool, error) {
	start := len(u.event)
	for {
		b, err := u.reader.ReadByte()
		if err != nil {
			return u.event[start:], false, err
		}
		if len(u.event) >= maxEventBytes {
			return nil, false, errEventTooLarge
		}
		u.event = append(u.event, b)
		if b != '\r' && b != '\n' {
			continue
		}
		end := len(u.event) - 1
		if b == '\r' {
			if next, err := u.reader.Peek(1); err == nil && next[0] == '\n' {
				if len(u.event) >= maxEventBytes {
					return nil, false, errEventTooLarge
				}
				_, _ = u.reader.ReadByte()
				u.event = append(u.event, '\n')
			}
		}
		return u.event[start:end], true, nil
	}
}

func (u *usageScanningStream) consume(line []byte) {
	if value, ok := bytes.CutPrefix(line, []byte("event:")); ok {
		u.eventError = bytes.Equal(bytes.TrimSpace(value), []byte("error"))
		return
	}
	value, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	if u.hasData {
		u.data = append(u.data, '\n')
	}
	u.data = append(u.data, bytes.TrimPrefix(value, []byte(" "))...)
	u.hasData = true
}

func (u *usageScanningStream) dispatch() error {
	if u.eventError {
		return errors.New("upstream stream error event")
	}
	if !u.hasData {
		return nil
	}
	data := bytes.TrimSpace(u.data)
	if bytes.Equal(data, []byte("[DONE]")) {
		u.done = true
		return nil
	}
	var chunk struct {
		Error   json.RawMessage `json:"error"`
		Usage   *core.Usage     `json:"usage"`
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &chunk) != nil {
		return nil
	}
	if len(chunk.Error) > 0 && !bytes.Equal(bytes.TrimSpace(chunk.Error), []byte("null")) {
		return errors.New("upstream stream error event")
	}
	if chunk.Usage != nil {
		u.req.Usage = *chunk.Usage
	}
	if u.digest == nil {
		u.digest = sha256.New()
	}
	for _, choice := range chunk.Choices {
		_, _ = u.digest.Write([]byte(choice.Delta.Content))
	}
	return nil
}

func (u *usageScanningStream) end(err error) {
	switch {
	case u.done:
		u.err = io.EOF
	case errors.Is(err, context.Canceled):
		u.err = err
	default:
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		u.failed, u.err = true, err
		u.ready = append(u.ready, streamErrorEvent...)
	}
}
