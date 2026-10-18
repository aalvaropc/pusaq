# Pusaq — project constitution

Pusaq is an intelligent, cost-aware AI gateway written in Go. Apps point their
`base_url` at Pusaq and get routing, rate limits, fallback, and cost accounting
without changing their code. Full design: [docs/design.md](docs/design.md).

Everything in this repository — code, comments, docs, commit messages, specs —
is written in **English**.

## Non-negotiable design rules

1. **No physical model names in code.** Models are addressed through
   configurable aliases only (`pusaq/fast`, `pusaq/best`, `pusaq/auto`).
   Physical names (and their retirement dates) live in configuration.
2. **Semantic cache keys are exhaustive.** The cache key always includes:
   tenant, model + version, system prompt, available tools, response format,
   temperature, language, and policy version. Anything less can leak one
   tenant's answer to another.
3. **New caches ship in shadow mode first.** A cache serves nothing until its
   precision has been measured against real responses; the similarity
   threshold τ is chosen for a precision target (≥ 0.99), never for hit-rate.
   The false-positive rate is always published next to the hit-rate.
4. **Limit ordering is fixed.** RPM limiting runs before the cache; TPM/budget
   reservation runs after model selection. Cache hits never consume TPM.
   TPM reconciliation runs in a `defer` — it must happen on success, error,
   timeout, and client cancellation alike.
5. **Rate-limit state mutations are atomic.** Bucket reads and decrements are
   single Redis Lua scripts — never read-modify-write from the gateway.
6. **Never claim guarantees the architecture cannot deliver.** With one Redis
   per region, limits are per-region during failover (bounded overrun, split
   quotas) — not "globally exact". Hard regional failure can drop in-flight
   SSE streams — never promise "zero dropped streams"; measure and report
   redirected, retried, and interrupted requests separately.
7. **OpenAI-compatible subset only.** Chat completions, SSE streaming, basic
   tool calling, token usage, normalized errors. Response header:
   `X-Pusaq-Model`. Nothing beyond the documented subset.
8. **It is a "policy filter", not a "prompt firewall".** Regex + heuristics do
   not earn the word firewall.
9. **Failure injection is deliberate.** Chaos experiments fail the readiness
   endpoint on purpose; deleting a service is not a valid health-check test.

## Workflow

Spec-driven via OpenSpec (`openspec/`). Every change follows:
proposal (`/opsx:propose`) → approved plan → vertical slice implementation →
**measured** verification (acceptance criteria are numbers: limiter exactness
under concurrency, cache precision/recall, failover gap and loss counts) →
review → archive (`/opsx:archive`).

- Slices are vertical and small ("RPM token bucket for one tenant, end-to-end,
  with a concurrency test"), never horizontal layers.
- Development runs locally (Redis in Docker, mock provider); cloud resources
  exist only for measurement runs, created and destroyed with Terraform per run.
- Repository files and GitHub metadata (issues, PR descriptions, commit
  messages) describe the system as it is, in the present tense, limits
  included. They carry no tracking material: roadmaps, phases, schedules,
  budgets, backlogs, known issues, future features, follow-ups, next steps,
  pending tasks — that lives outside the repository. Unbuilt behavior is "not
  implemented" or "out of scope for this change"; `make lint-docs` checks.
- Commits follow Conventional Commits. Never add Claude co-authorship.
