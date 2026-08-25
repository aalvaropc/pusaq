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
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p})

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
	tests := []struct {
		name    string
		payload string
	}{
		{"invalid JSON", `{not json`},
		{"missing model", `{"messages":[{"role":"user","content":"x"}]}`},
		{"empty messages", `{"model":"pusaq/fast","messages":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := okProvider()
			h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p})
			_, err := h(context.Background(), &core.Request{Payload: []byte(tt.payload)})
			var norm *core.Error
			if !errors.As(err, &norm) || norm.Code != core.CodeInvalidRequest {
				t.Fatalf("err = %v, want %s", err, core.CodeInvalidRequest)
			}
			if p.got.Model != "" {
				t.Fatal("provider was called for an invalid request")
			}
		})
	}
}

func TestCompletionsUnknownModelRejected(t *testing.T) {
	p := okProvider()
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p})
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
	h := CompletionsHandler(testRoutes(), map[string]provider.Provider{"mock": p})

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
