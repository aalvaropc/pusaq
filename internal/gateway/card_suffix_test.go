package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
)

func TestCardSuffixPolicyAndCache(t *testing.T) {
	const card = "4111 1111 1111 1111 12/27"
	for _, block := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, role := range []string{"user", "system"} {
				t.Run(fmt.Sprintf("block=%t/stream=%t/%s", block, stream, role), func(t *testing.T) {
					p := okProvider()
					p.sse = fakeSSE
					tokens := &fakeTokenLimiter{allowed: true}
					tenantPolicy := Policy{BlockSensitive: block}
					sp := &fakeSpend{err: errors.New("budget store unavailable")}
					if block {
						tenantPolicy.BudgetUSD = 10
						tenantPolicy.FailClosed = true
					}
					s, events, shadow := contentServer(t, p, CompletionsOptions{Policies: supportOnly(tenantPolicy), Tokens: tokens, Spend: sp})
					messages := `[{"role":"user","content":"card ` + card + `"}]`
					if role == "system" {
						messages = `[{"role":"system","content":"card ` + card + `"},{"role":"user","content":"hello"}]`
					}
					body := fmt.Sprintf(`{"model":"pusaq/best","stream":%t,"max_tokens":32,"messages":%s}`, stream, messages)
					rec := postAs(s, "sk-support", body)
					want := http.StatusOK
					if block {
						want = http.StatusForbidden
					}
					if rec.Code != want || len(*events) != 1 {
						t.Fatalf("status=%d events=%+v body=%s", rec.Code, *events, rec.Body.String())
					}
					event := (*events)[0]
					if !slices.Contains(event.PolicyFlags, "pii:card") || len(shadow.all()) != 0 {
						t.Fatalf("event=%+v shadow=%d", event, len(shadow.all()))
					}
					if block {
						if p.got.Model != "" || len(tokens.charges) != 0 || len(tokens.adjusts) != 0 || event.ErrorCode != core.CodePolicyDenied {
							t.Fatalf("blocked request called provider or charged tokens: %+v", event)
						}
					} else {
						if p.got.Model == "" || len(tokens.charges) != 1 || event.CacheSkip != "sensitive" || event.Outcome != core.OutcomeSuccess {
							t.Fatalf("log-mode request=%+v charges=%v", event, tokens.charges)
						}
					}
					serialized, err := json.Marshal(event)
					if err != nil || strings.Contains(string(serialized), card) || strings.Contains(rec.Body.String(), card) {
						t.Fatalf("matched card escaped in response or event: %v", err)
					}
				})
			}
		}
	}
}
