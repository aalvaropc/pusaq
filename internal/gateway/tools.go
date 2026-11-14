package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

const (
	maxTools      = 128
	maxToolsBytes = 256 << 10
)

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type rawRequest struct {
	top      map[string]json.RawMessage
	messages []map[string]json.RawMessage
}

func absent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

func paramPath(field string) string {
	parts := strings.Split(field, ".")
	for i, p := range parts[:len(parts)-1] {
		switch p {
		case "messages", "tools", "tool_calls":
			parts[i] = p + "[]"
		}
	}
	return strings.Join(parts, ".")
}

func validateTools(cr *completionRequest, raw rawRequest) error {
	if err := validateToolDefinitions(cr, raw.top["tools"]); err != nil {
		return err
	}
	if err := validateToolChoice(cr); err != nil {
		return err
	}
	return validateToolMessages(cr.Messages, raw.messages)
}

func validateToolDefinitions(cr *completionRequest, raw json.RawMessage) error {
	if absent(raw) {
		cr.Tools = nil
		return nil
	}
	switch {
	case len(cr.Tools) == 0:
		return core.InvalidField("tools", "tools must declare at least one tool")
	case len(cr.Tools) > maxTools:
		return core.InvalidField("tools", "tools must declare at most %d tools", maxTools)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil || compact.Len() > maxToolsBytes {
		return core.InvalidField("tools", "tools must take at most %d bytes", maxToolsBytes)
	}
	var strict []struct {
		Function struct {
			Strict json.RawMessage `json:"strict"`
		} `json:"function"`
	}
	_ = json.Unmarshal(raw, &strict)
	declared := make(map[string]bool, len(cr.Tools))
	for i := range cr.Tools {
		t := &cr.Tools[i]
		if t.Type != "function" {
			return core.InvalidField("tools[].type", "tools[].type must be function")
		}
		if !toolName.MatchString(t.Function.Name) {
			return core.InvalidField("tools[].function.name", "tools[].function.name must be 1 to 64 letters, digits, '_' or '-'")
		}
		if declared[t.Function.Name] {
			return core.InvalidField("tools[].function.name", "tool %q is declared more than once", t.Function.Name)
		}
		declared[t.Function.Name] = true
		if absent(t.Function.Parameters) {
			t.Function.Parameters = nil
		} else if bytes.TrimSpace(t.Function.Parameters)[0] != '{' {
			return core.InvalidField("tools[].function.parameters", "tools[].function.parameters must be a JSON object")
		}
		if i < len(strict) && set(strict[i].Function.Strict, "") {
			return core.InvalidField("tools[].function.strict", "tools[].function.strict is not supported")
		}
	}
	return nil
}

func validateToolChoice(cr *completionRequest) error {
	if cr.ParallelToolCalls != nil && len(cr.Tools) == 0 {
		return core.InvalidField("parallel_tool_calls", "parallel_tool_calls requires tools")
	}
	if absent(cr.ToolChoice) {
		return nil
	}
	if len(cr.Tools) == 0 {
		return core.InvalidField("tool_choice", "tool_choice requires tools")
	}
	errChoice := core.InvalidField("tool_choice", "tool_choice must be none, auto, required or a function object")
	var mode string
	if json.Unmarshal(cr.ToolChoice, &mode) == nil {
		switch mode {
		case "none", "auto", "required":
			cr.toolChoice = &provider.ToolChoice{Mode: mode}
			return nil
		}
		return errChoice
	}
	var forced struct {
		Type     string `json:"type"`
		Function *struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(cr.ToolChoice, &forced) != nil || forced.Type != provider.ToolChoiceFunction || forced.Function == nil {
		return errChoice
	}
	for _, t := range cr.Tools {
		if t.Function.Name == forced.Function.Name {
			cr.toolChoice = &provider.ToolChoice{Mode: provider.ToolChoiceFunction, Function: forced.Function.Name}
			return nil
		}
	}
	return core.InvalidField("tool_choice.function.name", "tool_choice.function.name must name a declared tool")
}

func validateToolMessages(messages []provider.Message, raw []map[string]json.RawMessage) error {
	answered := make(map[string]bool)
	for i, m := range messages {
		var fields map[string]json.RawMessage
		if i < len(raw) {
			fields = raw[i]
		}
		if m.Role == "function" {
			return core.InvalidField("messages[].role", "the function role is not supported; answer tool calls with tool messages")
		}
		if !absent(fields["tool_calls"]) {
			if m.Role != "assistant" {
				return core.InvalidField("messages[].tool_calls", "only assistant messages carry tool_calls")
			}
			if len(m.ToolCalls) == 0 {
				return core.InvalidField("messages[].tool_calls", "messages[].tool_calls must not be empty")
			}
			for _, call := range m.ToolCalls {
				if err := validateToolCall(call, answered); err != nil {
					return err
				}
				answered[call.ID] = false
			}
		}
		if m.Role != "tool" {
			if !absent(fields["tool_call_id"]) {
				return core.InvalidField("messages[].tool_call_id", "only tool messages carry tool_call_id")
			}
			continue
		}
		done, made := answered[m.ToolCallID]
		switch {
		case m.ToolCallID == "":
			return core.InvalidField("messages[].tool_call_id", "tool messages require tool_call_id")
		case !made:
			return core.InvalidField("messages[].tool_call_id", "tool_call_id must answer a tool call made earlier in the conversation")
		case done:
			return core.InvalidField("messages[].tool_call_id", "each tool call is answered once")
		}
		answered[m.ToolCallID] = true
		if content := bytes.TrimSpace(fields["content"]); len(content) == 0 || content[0] != '"' {
			return core.InvalidField("messages[].content", "tool message content must be a string")
		}
	}
	return nil
}

func validateToolCall(call provider.ToolCall, made map[string]bool) error {
	if call.ID == "" {
		return core.InvalidField("messages[].tool_calls[].id", "messages[].tool_calls[].id is required")
	}
	if _, dup := made[call.ID]; dup {
		return core.InvalidField("messages[].tool_calls[].id", "tool call IDs must be unique")
	}
	if call.Type != "function" {
		return core.InvalidField("messages[].tool_calls[].type", "messages[].tool_calls[].type must be function")
	}
	if !toolName.MatchString(call.Function.Name) {
		return core.InvalidField("messages[].tool_calls[].function.name", "messages[].tool_calls[].function.name must be 1 to 64 letters, digits, '_' or '-'")
	}
	return nil
}

func toolsBytes(tools []provider.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	b, _ := json.Marshal(tools)
	return len(b)
}

func toolsKey(cr completionRequest) string {
	if len(cr.Tools) == 0 {
		return "none"
	}
	tools := make([]any, len(cr.Tools))
	for i, t := range cr.Tools {
		tools[i] = map[string]any{
			"type": t.Type,
			"function": map[string]any{
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  canonical(t.Function.Parameters),
			},
		}
	}
	b, _ := json.Marshal(map[string]any{
		"tools":               tools,
		"tool_choice":         cr.toolChoice,
		"parallel_tool_calls": cr.ParallelToolCalls,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func canonical(raw json.RawMessage) any {
	if absent(raw) {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return string(raw)
	}
	return v
}
