package gateway

import (
	"strconv"
	"strings"

	"github.com/aalvaropc/pusaq/internal/policy"
	"github.com/aalvaropc/pusaq/internal/provider"
)

// maxCacheTemperature is the highest explicit sampling temperature a
// request may carry and still be cache-eligible: above it the answer is
// creative generation, not a repeatable one (docs/design.md §5.4). An
// absent temperature is eligible — the provider default is one namespace
// of its own — so shadow measurement still sees ordinary traffic.
const maxCacheTemperature = 0.7

// cacheShape is what the semantic cache needs from an eligible request:
// namespace material and the embedded text.
type cacheShape struct {
	SystemPrompt string
	UserContent  string
	Tools        string
	// Temperature is the formatted explicit value, or "none".
	Temperature string
	MaxTokens   string
}

// cacheable reports whether a request is structurally eligible for the
// semantic cache (docs/design.md §5.4): exactly one user message, optionally preceded by
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

// classifyContent runs each content-rule family where it means something:
// sensitive data anywhere; instruction overrides outside what the application
// wrote (system prompt, tool definitions), tool results included; time
// dependence in the user's own words.
func classifyContent(cr completionRequest) policy.Flags {
	var all, nonSystem, user []string
	for _, t := range cr.Tools {
		all = append(all, t.Function.Name, t.Function.Description, string(t.Function.Parameters))
	}
	for _, m := range cr.Messages {
		all = append(all, m.Content)
		if m.Role != "system" {
			nonSystem = append(nonSystem, m.Content)
		}
		if m.Role == "user" {
			user = append(user, m.Content)
		}
		for _, call := range m.ToolCalls {
			all = append(all, call.Function.Arguments)
			nonSystem = append(nonSystem, call.Function.Arguments)
		}
	}
	return policy.Flags{
		Sensitive: policy.Sensitive(all...),
		Injection: policy.Injection(nonSystem...),
		Temporal:  policy.Temporal(user...),
	}
}

// cacheEligibility decides once per request whether the semantic cache may
// see it and, when not, the first reason in a fixed order: the tenant opted
// out, the shape is multi-turn, the content carries sensitive data, an
// instruction override, a time dependence, or a high explicit temperature.
// The reason travels on the cost event as cache_skip.
func cacheEligibility(cr completionRequest, p Policy, flags policy.Flags) (cacheShape, string) {
	if p.CacheOff {
		return cacheShape{}, "tenant_off"
	}
	systemPrompt, userContent, ok := cacheable(cr.Messages)
	if !ok {
		return cacheShape{}, "multi_turn"
	}
	switch {
	case len(flags.Sensitive) > 0:
		return cacheShape{}, "sensitive"
	case len(flags.Injection) > 0:
		return cacheShape{}, "injection"
	case len(flags.Temporal) > 0:
		return cacheShape{}, "temporal"
	}
	temperature := "none"
	if cr.Temperature != nil {
		if *cr.Temperature > maxCacheTemperature {
			return cacheShape{}, "temperature"
		}
		temperature = strconv.FormatFloat(*cr.Temperature, 'g', -1, 64)
	}
	maxTokens := "none"
	if cr.MaxTokens != nil {
		maxTokens = strconv.Itoa(*cr.MaxTokens)
	}
	return cacheShape{SystemPrompt: systemPrompt, UserContent: userContent, Tools: toolsKey(cr), Temperature: temperature, MaxTokens: maxTokens}, ""
}
