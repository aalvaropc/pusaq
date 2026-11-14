package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/mockprovider"
	"github.com/aalvaropc/pusaq/internal/provider"
)

type answered struct {
	content string
	call    provider.ToolCall
	finish  string
}

func mockUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mock, err := mockprovider.NewServer(discardLogger(), mockprovider.Options{StreamChunkDelay: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(mock)
	t.Cleanup(upstream.Close)
	return upstream
}

func readAnswer(t *testing.T, stream bool, body []byte) answered {
	t.Helper()
	var out answered
	if !stream {
		var resp completionResponse
		if err := json.Unmarshal(body, &resp); err != nil || len(resp.Choices) != 1 {
			t.Fatalf("body %s: %v", body, err)
		}
		c := resp.Choices[0]
		out.content, out.finish = c.Message.Content, c.FinishReason
		if len(c.Message.ToolCalls) == 1 {
			out.call = c.Message.ToolCalls[0]
		}
		return out
	}
	if !strings.HasSuffix(string(body), "data: [DONE]\n\n") {
		t.Fatalf("stream did not end with [DONE]: %q", body)
	}
	for _, line := range strings.Split(string(body), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string                `json:"id"`
						Type     string                `json:"type"`
						Function provider.FunctionCall `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk %q: %v", payload, err)
		}
		for _, c := range chunk.Choices {
			out.content += c.Delta.Content
			if c.FinishReason != "" {
				out.finish = c.FinishReason
			}
			for _, d := range c.Delta.ToolCalls {
				if d.ID != "" {
					out.call.ID, out.call.Type = d.ID, d.Type
				}
				out.call.Function.Name += d.Function.Name
				out.call.Function.Arguments += d.Function.Arguments
			}
		}
	}
	return out
}

func TestToolRoundTripThroughTheMock(t *testing.T) {
	upstream := mockUpstream(t)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			tokens := &fakeTokenLimiter{allowed: true}
			var events []core.CostEvent
			pipeline := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, discardLogger()), RequestID(), Auth(nil))
			completions := CompletionsHandler(testRoutes(),
				map[string]provider.Provider{"mock": provider.NewHTTP("mock", upstream.URL+"/v1", time.Second, discardLogger())},
				CompletionsOptions{Tokens: tokens, ReserveDefault: 100, Prices: testPrices(), Logger: discardLogger()})
			s := NewServer(discardLogger(), pipeline, completions)
			send := func(messages string) answered {
				body := fmt.Sprintf(`{"model":"pusaq/fast","stream":%t,"messages":%s,"tools":[%s]}`, stream, messages, weatherTool)
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
				}
				return readAnswer(t, stream, rec.Body.Bytes())
			}

			first := send(`[{"role":"user","content":"weather in Lima"}]`)
			want := provider.ToolCall{ID: "call_mock_1", Type: "function", Function: provider.FunctionCall{Name: "get_weather", Arguments: `{"city":"mock"}`}}
			if first.call != want || first.finish != "tool_calls" || first.content != "" {
				t.Fatalf("first answer = %+v, want a call to get_weather", first)
			}

			call, err := json.Marshal(provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{first.call}})
			if err != nil {
				t.Fatal(err)
			}
			second := send(`[{"role":"user","content":"weather in Lima"},` + string(call) + `,{"role":"tool","tool_call_id":"` + first.call.ID + `","content":"18C"}]`)
			if !strings.HasPrefix(second.content, "The tool answered") || second.finish != "stop" || second.call.ID != "" {
				t.Fatalf("second answer = %+v, want the mock's text after a tool result", second)
			}

			tools := len(weatherTool) + 2
			wantCharges := []float64{
				float64((len("user")+len("weather in Lima")+tools+3)/4) + 100,
				float64((len("user")+len("assistant")+len("tool")+len("weather in Lima")+len("get_weather")+len(`{"city":"mock"}`)+len("18C")+tools+3)/4) + 100,
			}
			if len(tokens.charges) != 2 || tokens.charges[0] != wantCharges[0] || tokens.charges[1] != wantCharges[1] {
				t.Fatalf("charges = %v, want %v", tokens.charges, wantCharges)
			}
			if len(events) != 2 || len(tokens.adjusts) != 2 {
				t.Fatalf("events = %d, adjusts = %v, want two of each", len(events), tokens.adjusts)
			}
			for i, e := range events {
				if e.Outcome != core.OutcomeSuccess || e.Usage != (core.Usage{PromptTokens: 12, CompletionTokens: 11}) {
					t.Fatalf("event %d = %+v, want the provider's usage", i, e)
				}
				if want := (core.Cost{USD: 5.6e-6, TableVersion: "v-test"}); e.Cost != want {
					t.Fatalf("event %d cost = %+v, want %+v priced from usage", i, e.Cost, want)
				}
				if tokens.adjusts[i] != tokens.charges[i]-23 {
					t.Fatalf("adjusts = %v, want each charge reconciled against 23 used tokens", tokens.adjusts)
				}
			}
		})
	}
}

func TestMockToolCallStreamReachesTheClientUnchanged(t *testing.T) {
	upstream := mockUpstream(t)
	direct, err := http.Post(upstream.URL+"/v1/chat/completions", "application/json", strings.NewReader(
		`{"model":"mock-model","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"weather in Lima"}],"tools":[`+weatherTool+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := io.ReadAll(direct.Body)
	_ = direct.Body.Close()
	if !strings.Contains(string(want), `"tool_calls"`) {
		t.Fatalf("the mock did not stream a tool call: %s", want)
	}

	completions := CompletionsHandler(testRoutes(),
		map[string]provider.Provider{"mock": provider.NewHTTP("mock", upstream.URL+"/v1", time.Second, discardLogger())},
		CompletionsOptions{Logger: discardLogger()})
	s := NewServer(discardLogger(), core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil)), completions)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(toolRequest(`,"stream":true`))))
	if rec.Code != http.StatusOK || rec.Body.String() != string(want) {
		t.Fatalf("status = %d, client stream differs from the mock's:\n got %q\nwant %q", rec.Code, rec.Body.String(), want)
	}
}
