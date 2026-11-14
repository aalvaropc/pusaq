package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestToolFieldsEncodeAsOpenAI(t *testing.T) {
	off := false
	req := ChatRequest{
		Model: "mock-model",
		Messages: []Message{
			{Role: "user", Content: "weather in Lima"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Lima"}`}}}},
			{Role: "tool", Content: "18C", ToolCallID: "call_1"},
			{Role: "assistant", Content: ""},
		},
		Tools: []Tool{{Type: "function", Function: Function{
			Name:       "get_weather",
			Parameters: json.RawMessage(`{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"number"}}}`),
		}}},
		ToolChoice:        &ToolChoice{Mode: ToolChoiceFunction, Function: "get_weather"},
		ParallelToolCalls: &off,
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"mock-model","messages":[` +
		`{"role":"user","content":"weather in Lima"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Lima\"}"}}]},` +
		`{"role":"tool","content":"18C","tool_call_id":"call_1"},` +
		`{"role":"assistant","content":""}],` +
		`"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"number"}}}}}],` +
		`"tool_choice":{"type":"function","function":{"name":"get_weather"}},"parallel_tool_calls":false}`
	if string(got) != want {
		t.Fatalf("encoded request\n got %s\nwant %s", got, want)
	}

	for mode, want := range map[string]string{"none": `"none"`, "auto": `"auto"`, "required": `"required"`} {
		b, err := json.Marshal(ToolChoice{Mode: mode})
		if err != nil || string(b) != want {
			t.Fatalf("tool_choice %s = %s (%v), want %s", mode, b, err, want)
		}
	}
}

func TestCompleteDecodesToolCalls(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"cmpl-1","model":"mock-model","choices":[{"index":0,`+
			`"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Lima\"}"}}]},`+
			`"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	defer upstream.Close()

	resp, err := NewHTTP("mock", upstream.URL, time.Second, discardLogger()).Complete(context.Background(), chatReq())
	if err != nil {
		t.Fatal(err)
	}
	c := resp.Choices[0]
	if c.FinishReason != "tool_calls" || len(c.Message.ToolCalls) != 1 {
		t.Fatalf("choice = %+v", c)
	}
	if call := c.Message.ToolCalls[0]; call.ID != "call_1" || call.Type != "function" || call.Function.Name != "get_weather" || call.Function.Arguments != `{"city":"Lima"}` {
		t.Fatalf("tool call = %+v", call)
	}
	b, err := json.Marshal(c.Message)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Lima\"}"}}]}`; string(b) != want {
		t.Fatalf("message = %s, want %s", b, want)
	}
}
