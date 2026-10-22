## 1. Documentation

- [x] 1.1 Refresh the README and add docs navigation; verify a README word count below 300 and working local links.
- [x] 1.2 Align architecture, API, configuration and operations with source, Compose and workflows; verify admission ordering, all configuration variables, security boundaries and runnable command syntax.
- [x] 1.3 Index all nine dated measurement reports and correct stale contextual references; verify historical dates and result tables remain intact.

## 2. Verification

- [x] 2.1 Run documentation lint, whitespace and strict OpenSpec checks; validate rendered Markdown and the local mock request, recording any environment limitation.

## Verification results

- README: 272 whitespace-separated words; GitHub GFM rendering contains three badges, one capability table and two fenced examples.
- Documentation: 16 Markdown files, 92 local links and fragments, eight shell blocks checked with Bash syntax validation, and two JSON request examples parsed successfully.
- Configuration: all 40 environment setting names match the configuration source; resolved Compose settings match the documented development mode, quotas, cache mode and loopback bindings.
- Measurements: all nine reports are indexed; historical result-table rows are preserved. The breaker environment row identifies its one-probe setting as historical.
- `make lint-docs`, `git diff --check` and strict OpenSpec validation pass (11 items: ten specs and this editorial change).
- Local smoke: readiness returns `ready` and the README request returns a completion with 12 prompt and 11 completion tokens from the mock inside Compose. The host-published localhost endpoint times out from this execution environment after four seconds; that path is not verified here.
