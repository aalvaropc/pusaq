package mockprovider

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const (
	toolResultContent   = "The tool answered; this is the Pusaq mock provider's canned reply."
	argumentFragmentLen = 8
)

type completionRequest struct {
	Model         string `json:"model"`
	Stream        bool   `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Messages []struct {
		Role      string            `json:"role"`
		ToolCalls []json.RawMessage `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice json.RawMessage `json:"tool_choice"`
}

type toolCall struct {
	id, name, arguments string
}

type answer struct {
	content string
	call    *toolCall
}

func (r completionRequest) answer() answer {
	last := ""
	made := 0
	for _, m := range r.Messages {
		last = m.Role
		made += len(m.ToolCalls)
	}
	if last == "tool" {
		return answer{content: toolResultContent}
	}
	var mode string
	if len(r.Tools) == 0 || (json.Unmarshal(r.ToolChoice, &mode) == nil && mode == "none") {
		return answer{content: cannedContent}
	}
	tool := r.Tools[0].Function
	var forced struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(r.ToolChoice, &forced) == nil {
		for _, t := range r.Tools {
			if t.Function.Name == forced.Function.Name {
				tool = t.Function
			}
		}
	}
	return answer{call: &toolCall{
		id:        fmt.Sprintf("call_mock_%d", made+1),
		name:      tool.Name,
		arguments: mockArguments(tool.Parameters),
	}}
}

func mockArguments(schema json.RawMessage) string {
	var s struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &s)
	args := make(map[string]any, len(s.Properties))
	for name, p := range s.Properties {
		t, _ := p.Type.(string)
		switch t {
		case "string":
			args[name] = "mock"
		case "number", "integer":
			args[name] = 1
		case "boolean":
			args[name] = true
		case "array":
			args[name] = []any{}
		case "object":
			args[name] = map[string]any{}
		default:
			args[name] = nil
		}
	}
	b, _ := json.Marshal(args)
	return string(b)
}

func (a answer) finishReason() string {
	if a.call != nil {
		return "tool_calls"
	}
	return "stop"
}

func (a answer) message() map[string]any {
	if a.call == nil {
		return map[string]any{"role": "assistant", "content": a.content}
	}
	return map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
		"id": a.call.id, "type": "function", "function": map[string]any{"name": a.call.name, "arguments": a.call.arguments},
	}}}
}

func (c toolCall) deltas() []map[string]any {
	out := []map[string]any{{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
		"index": 0, "id": c.id, "type": "function", "function": map[string]any{"name": c.name, "arguments": ""},
	}}}}
	for rest := c.arguments; rest != ""; {
		n := min(argumentFragmentLen, len(rest))
		for n > 1 && n < len(rest) && !utf8.RuneStart(rest[n]) {
			n--
		}
		out = append(out, map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "function": map[string]any{"arguments": rest[:n]},
		}}})
		rest = rest[n:]
	}
	return out
}
