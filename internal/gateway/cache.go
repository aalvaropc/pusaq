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
// namespace material (system prompt, temperature) and the embedded text.
type cacheShape struct {
	SystemPrompt string
	UserContent  string
	// Temperature is the formatted explicit value, or "none".
	Temperature string
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

// classifyContent runs the content rules over the request's messages, each
// family on the roles where it means something: sensitive data anywhere;
// instruction-override phrasing outside the system prompt (that is where
// the application legitimately sets the rules); time dependence in the
// user's own words (system prompts routinely state today's date).
func classifyContent(messages []provider.Message) policy.Flags {
	var all, nonSystem, user []string
	for _, m := range messages {
		all = append(all, m.Content)
		if m.Role != "system" {
			nonSystem = append(nonSystem, m.Content)
		}
		if m.Role == "user" {
			user = append(user, m.Content)
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
	return cacheShape{SystemPrompt: systemPrompt, UserContent: userContent, Temperature: temperature}, ""
}
