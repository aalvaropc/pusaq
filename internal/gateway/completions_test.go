package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

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
	}{
		{"invalid JSON", `{not json`, "request body is not valid JSON"},
		{"missing model", `{` + msgs + `}`, "model is required"},
		{"empty messages", `{"model":"pusaq/fast","messages":[]}`, "messages must not be empty"},
		{"negative max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":-5}`, "max_tokens must be a positive integer"},
		{"zero max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":0}`, "max_tokens must be a positive integer"},
		{"string max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":"32"}`, "max_tokens must be a positive integer"},
		{"oversized max_tokens", `{"model":"pusaq/fast",` + msgs + `,"max_tokens":9223372036854775808}`, "max_tokens must be a positive integer"},
		{"content parts", `{"model":"pusaq/fast","messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]}`, "content parts are not supported"},
		{"tools", `{"model":"pusaq/fast",` + msgs + `,"tools":[{"type":"function"}]}`, "tools is not supported"},
		{"tool_choice", `{"model":"pusaq/fast",` + msgs + `,"tool_choice":"auto"}`, "tool_choice is not supported"},
		{"response_format", `{"model":"pusaq/fast",` + msgs + `,"response_format":{"type":"json_object"}}`, "response_format is not supported"},
		{"n above one", `{"model":"pusaq/fast",` + msgs + `,"n":2}`, "n is not supported"},
		{"logprobs", `{"model":"pusaq/fast",` + msgs + `,"logprobs":true}`, "logprobs is not supported"},
		{"message tool_calls", `{"model":"pusaq/fast","messages":[{"role":"assistant","content":"","tool_calls":[{"id":"c"}]}]}`, "messages[].tool_calls is not supported"},
		{"message tool_call_id", `{"model":"pusaq/fast","messages":[{"role":"tool","content":"x","tool_call_id":"c"}]}`, "messages[].tool_call_id is not supported"},
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
