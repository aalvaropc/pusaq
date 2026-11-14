package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

// fakeProvider records the request it received and returns a fixed result.
type fakeProvider struct {
	name string
	got  provider.ChatRequest
	resp provider.ChatResponse
	sse  string // raw SSE body served by StreamComplete
	err  error
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Complete(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	f.got = req
	return f.resp, f.err
}

func (f *fakeProvider) StreamComplete(_ context.Context, req provider.ChatRequest) (io.ReadCloser, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.sse)), nil
}

func testRoutes() map[string]Route {
	return map[string]Route{
		"pusaq/fast": {Provider: "mock", Model: "mock-model"},
	}
}

func okProvider() *fakeProvider {
	return &fakeProvider{
		name: "mock",
		resp: provider.ChatResponse{
			ID:    "cmpl-1",
			Model: "mock-model",
			Choices: []provider.Choice{{
				Message:      provider.Message{Role: "assistant", Content: "hi"},
				FinishReason: "stop",
			}},
			Usage: core.Usage{PromptTokens: 5, CompletionTokens: 2},
		},
	}
}

func validPayload() []byte {
	return []byte(`{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`)
}

func TestCompletionsRoundTrip(t *testing.T) {
	p := okProvider()
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p}, CompletionsOptions{Logger: discardLogger()})

	req := &core.Request{Op: "chat.completions", Payload: validPayload()}
	resp, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if p.got.Model != "mock-model" {
		t.Fatalf("provider received model %q, want the physical model", p.got.Model)
	}
	if p.got.MaxTokens != 32 || len(p.got.Messages) != 1 {
		t.Fatalf("provider request = %+v", p.got)
	}
	if resp.Header[ServedModelHeader] != "mock-model" {
		t.Fatalf("%s = %q", ServedModelHeader, resp.Header[ServedModelHeader])
	}
	if req.RequestedModel != "pusaq/fast" || req.ServedModel != "mock-model" {
		t.Fatalf("envelope models = %q/%q", req.RequestedModel, req.ServedModel)
	}
	if req.Usage.PromptTokens != 5 || req.Usage.CompletionTokens != 2 {
		t.Fatalf("envelope usage = %+v", req.Usage)
	}

	var body completionResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("response body: %v", err)
	}
	if body.Object != "chat.completion" || body.Usage.TotalTokens != 7 {
		t.Fatalf("body = %+v", body)
	}
}

func TestCompletionsValidation(t *testing.T) {
	const msgs = `"messages":[{"role":"user","content":"x"}]`
	tests := []struct {
		name    string
		payload string
		message string
		param   string
	}{
		{"invalid JSON", `{not json`, "request body is not valid JSON", ""},
		{"missing model", `{` + msgs + `}`, "model is required", "model"},
		{"empty messages", `{"model":"pusaq/fast","messages":[]}`, "messages must not be empty", "messages"},
		{"negative max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":-5}`, "max_tokens must be a positive integer", "max_tokens"},
		{"zero max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":0}`, "max_tokens must be a positive integer", "max_tokens"},
		{"string max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":"32"}`, "max_tokens must be a positive integer", "max_tokens"},
		{"oversized max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":9223372036854775808}`, "max_tokens must be a positive integer", "max_tokens"},
		{"content parts", `{"model":"pusaq/fast","messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]}`, "content parts are not supported", "messages[].content"},
		{"mistyped field", `{"model":"pusaq/fast",` + msgs + `,"temperature":"hot"}`, "temperature has the wrong type", "temperature"},
		{"functions", `{"model":"pusaq/fast",` + msgs + `,"functions":[{"name":"f"}]}`, "functions is not supported", "functions"},
		{"function_call", `{"model":"pusaq/fast",` + msgs + `,"function_call":"auto"}`, "function_call is not supported", "function_call"},
		{"response_format", `{"model":"pusaq/fast",` + msgs + `,"response_format":{"type":"json_object"}}`, "response_format is not supported", "response_format"},
		{"n above one", `{"model":"pusaq/fast",` + msgs + `,"n":2}`, "n is not supported", "n"},
		{"logprobs", `{"model":"pusaq/fast",` + msgs + `,"logprobs":true}`, "logprobs is not supported", "logprobs"},
		{"message function_call", `{"model":"pusaq/fast","messages":[{"role":"assistant","content":"","function_call":{"name":"f","arguments":"{}"}}]}`, "messages[].function_call is not supported", "messages[].function_call"},
		{"unknown model", `{"model":"pusaq/none",` + msgs + `}`, `unknown model "pusaq/none"`, "model"},
		{"non-string feature tag", `{"model":"pusaq/fast",` + msgs + `,"metadata":{"feature_tag":7}}`, "metadata.feature_tag must be a string", "metadata.feature_tag"},
		{"malformed feature tag", `{"model":"pusaq/fast",` + msgs + `,"metadata":{"feature_tag":"a b"}}`, "metadata.feature_tag must be at most", "metadata.feature_tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := okProvider()
			h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p}, CompletionsOptions{Logger: discardLogger()})
			_, err := h(context.Background(), &core.Request{Payload: []byte(tt.payload)})
			var norm *core.Error
			if !errors.As(err, &norm) || norm.Code != core.CodeInvalidRequest {
				t.Fatalf("err = %v, want %s", err, core.CodeInvalidRequest)
			}
			if !strings.Contains(norm.Message, tt.message) {
				t.Fatalf("message = %q, want it to contain %q", norm.Message, tt.message)
			}
			if norm.Param != tt.param {
				t.Fatalf("param = %q, want %q", norm.Param, tt.param)
			}
			if p.got.Model != "" {
				t.Fatal("provider was called for an invalid request")
			}
		})
	}
}

func TestHarmlessFieldsAreAccepted(t *testing.T) {
	payload := `{"model":"pusaq/fast","messages":[{"role":"user","content":"x","name":"ana"}],` +
		`"user":"u-1","seed":7,"n":1,"logprobs":false,"tools":null,"modalities":["text"],"stop":["\n"]}`
	p := okProvider()
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p}, CompletionsOptions{Logger: discardLogger()})
	if _, err := h(context.Background(), &core.Request{Payload: []byte(payload)}); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if p.got.Model != "mock-model" {
		t.Fatal("provider was not called for a request with harmless fields")
	}
}

func TestCompletionCarriesCreatedAndFingerprint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"cmpl-9","object":"chat.completion","created":1700000000,"model":"mock-model",`+
			`"system_fingerprint":"fp_test","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)

	tests := []struct {
		name            string
		provider        provider.Provider
		wantCreated     int64
		wantFingerprint string
	}{
		{"provider values pass through", provider.NewHTTP("mock", upstream.URL+"/v1", time.Second, discardLogger()), 1700000000, "fp_test"},
		{"gateway time without provider values", okProvider(), 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": tt.provider}, CompletionsOptions{Logger: discardLogger()})
			before := time.Now().Unix()
			resp, err := h(context.Background(), &core.Request{Payload: validPayload()})
			after := time.Now().Unix()
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(resp.Body, &body); err != nil {
				t.Fatal(err)
			}
			var created int64
			if err := json.Unmarshal(body["created"], &created); err != nil {
				t.Fatalf("created = %s: %v", body["created"], err)
			}
			if tt.wantCreated != 0 && created != tt.wantCreated {
				t.Fatalf("created = %d, want %d", created, tt.wantCreated)
			}
			if tt.wantCreated == 0 && (created < before || created > after) {
				t.Fatalf("created = %d, want the gateway's time in [%d, %d]", created, before, after)
			}
			fingerprint, present := body["system_fingerprint"]
			if tt.wantFingerprint == "" && present {
				t.Fatalf("system_fingerprint = %s, want it absent", fingerprint)
			}
			if tt.wantFingerprint != "" && string(fingerprint) != `"`+tt.wantFingerprint+`"` {
				t.Fatalf("system_fingerprint = %s, want %q", fingerprint, tt.wantFingerprint)
			}
		})
	}
}

func TestCompletionsUnknownModelRejected(t *testing.T) {
	p := okProvider()
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p}, CompletionsOptions{Logger: discardLogger()})
	_, err := h(context.Background(), &core.Request{
		Payload: []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}`),
	})
	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeInvalidRequest {
		t.Fatalf("err = %v, want %s", err, core.CodeInvalidRequest)
	}
	if p.got.Model != "" {
		t.Fatal("provider was called for an unknown model")
	}
}

func TestCompletionsUpstreamErrorPropagates(t *testing.T) {
	p := okProvider()
	p.err = core.NewError(core.CodeUpstream, "upstream provider returned status 500")
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p}, CompletionsOptions{Logger: discardLogger()})

	req := &core.Request{Payload: validPayload()}
	_, err := h(context.Background(), req)

	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeUpstream {
		t.Fatalf("err = %v, want %s", err, core.CodeUpstream)
	}
	if norm.Status() != http.StatusBadGateway {
		t.Fatalf("Status() = %d, want 502", norm.Status())
	}
	// Models were resolved before the failure — accounting still sees them.
	if req.ServedModel != "mock-model" {
		t.Fatalf("ServedModel = %q after upstream error", req.ServedModel)
	}
}
