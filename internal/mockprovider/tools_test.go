package mockprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const twoTools = `"tools":[` +
	`{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}},` +
	`{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"integer"},"flag":{"type":"boolean"},"tags":{"type":"array"},"meta":{"type":"object"},"either":{"type":["string","null"]},"ratio":{"type":"number"}}}}}]`

type mockMessage struct {
	Role      string  `json:"role"`
	Content   *string `json:"content"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

func post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.sleep = func(time.Duration) {}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	return rec
}

func buffered(t *testing.T, body string) (mockMessage, string) {
	t.Helper()
	var resp struct {
		Choices []struct {
			Message      mockMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(post(t, body).Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 || resp.Usage.TotalTokens != 23 {
		t.Fatalf("response = %+v, want one choice with usage", resp)
	}
	return resp.Choices[0].Message, resp.Choices[0].FinishReason
}

func request(messages, fields string) string {
	return `{"model":"mock-model","messages":` + messages + `,` + twoTools + fields + `}`
}

const userTurn = `[{"role":"user","content":"x"}]`

func TestMockCallsTheForcedOrFirstTool(t *testing.T) {
	for _, tt := range []struct {
		name, fields, tool, arguments string
	}{
		{"first tool by default", ``, "get_weather", `{"city":"mock"}`},
		{"auto", `,"tool_choice":"auto"`, "get_weather", `{"city":"mock"}`},
		{"required", `,"tool_choice":"required"`, "get_weather", `{"city":"mock"}`},
		{"forced", `,"tool_choice":{"type":"function","function":{"name":"lookup"}}`, "lookup",
			`{"alpha":1,"either":null,"flag":true,"meta":{},"ratio":1,"tags":[],"zeta":"mock"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			msg, finish := buffered(t, request(userTurn, tt.fields))
			if finish != "tool_calls" || msg.Content != nil || len(msg.ToolCalls) != 1 {
				t.Fatalf("message = %+v finish = %q, want one tool call with null content", msg, finish)
			}
			call := msg.ToolCalls[0]
			if call.ID != "call_mock_1" || call.Type != "function" || call.Function.Name != tt.tool || call.Function.Arguments != tt.arguments {
				t.Fatalf("tool call = %+v", call)
			}
		})
	}
	msg, _ := buffered(t, `{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[{"type":"function","function":{"name":"ping"}}]}`)
	if msg.ToolCalls[0].Function.Arguments != `{}` {
		t.Fatalf("arguments without a schema = %q, want {}", msg.ToolCalls[0].Function.Arguments)
	}
}

func TestMockToolCallIDsStayUniqueInAConversation(t *testing.T) {
	loop := `[{"role":"user","content":"x"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_mock_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_mock_1","content":"18C"},{"role":"assistant","content":"18C"},{"role":"user","content":"and Cusco?"}]`
	msg, _ := buffered(t, request(loop, ""))
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_mock_2" {
		t.Fatalf("message = %+v, want call_mock_2", msg)
	}
}

func TestMockAnswersTextAfterAToolResultOrWithoutToolChoice(t *testing.T) {
	afterTool := `[{"role":"user","content":"x"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_mock_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_mock_1","content":"18C"}]`
	for name, tt := range map[string]struct{ body, content string }{
		"after a tool result": {request(afterTool, ""), toolResultContent},
		"tool_choice none":    {request(userTurn, `,"tool_choice":"none"`), cannedContent},
		"without tools":       {`{"model":"m","messages":[{"role":"user","content":"x"}]}`, cannedContent},
	} {
		msg, finish := buffered(t, tt.body)
		if finish != "stop" || len(msg.ToolCalls) != 0 || msg.Content == nil || *msg.Content != tt.content {
			t.Fatalf("%s: message = %+v finish = %q", name, msg, finish)
		}
	}
}

type streamed struct {
	firstID, firstName string
	argumentDeltas     []string
	text               string
	finish             string
	usage              bool
	done               bool
}

func readStream(t *testing.T, body string) streamed {
	t.Helper()
	var out streamed
	for _, line := range strings.Split(post(t, body).Body.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			out.done = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string  `json:"name"`
							Arguments *string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct{} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk %q: %v", payload, err)
		}
		out.usage = out.usage || chunk.Usage != nil
		for _, c := range chunk.Choices {
			out.text += c.Delta.Content
			if c.FinishReason != nil {
				out.finish = *c.FinishReason
			}
			for _, call := range c.Delta.ToolCalls {
				if call.Index != 0 {
					t.Fatalf("tool-call index = %d", call.Index)
				}
				if call.ID != "" {
					out.firstID, out.firstName = call.ID, call.Function.Name
					continue
				}
				out.argumentDeltas = append(out.argumentDeltas, *call.Function.Arguments)
			}
		}
	}
	return out
}

func TestMockStreamsToolCallArgumentsInFragments(t *testing.T) {
	forced := `,"tool_choice":{"type":"function","function":{"name":"lookup"}}`
	msg, _ := buffered(t, request(userTurn, forced))
	s := readStream(t, request(userTurn, forced+`,"stream":true,"stream_options":{"include_usage":true}`))
	if s.firstID != "call_mock_1" || s.firstName != "lookup" || s.finish != "tool_calls" || !s.usage || !s.done || s.text != "" {
		t.Fatalf("stream = %+v", s)
	}
	if len(s.argumentDeltas) < 2 || strings.Join(s.argumentDeltas, "") != msg.ToolCalls[0].Function.Arguments {
		t.Fatalf("argument deltas %q do not rebuild %q", s.argumentDeltas, msg.ToolCalls[0].Function.Arguments)
	}
	for _, d := range s.argumentDeltas {
		if len(d) > argumentFragmentLen {
			t.Fatalf("fragment %q is longer than %d bytes", d, argumentFragmentLen)
		}
	}

	unicode := `{"model":"m","stream":true,"messages":[{"role":"user","content":"x"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"properties":{"ñandú_año":{"type":"string"}}}}}]}`
	u := readStream(t, unicode)
	for _, d := range u.argumentDeltas {
		if !utf8.ValidString(d) {
			t.Fatalf("fragment %q splits a character", d)
		}
	}
	if strings.Join(u.argumentDeltas, "") != `{"ñandú_año":"mock"}` {
		t.Fatalf("argument deltas = %q", u.argumentDeltas)
	}

	afterTool := `[{"role":"user","content":"x"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_mock_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_mock_1","content":"18C"}]`
	text := readStream(t, request(afterTool, `,"stream":true`))
	if text.text != toolResultContent || text.finish != "stop" || text.firstID != "" || !text.done {
		t.Fatalf("stream after a tool result = %+v", text)
	}
}
