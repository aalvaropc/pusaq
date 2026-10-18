# Tasks: add-request-validation

## 1. Request validation

- [x] 1.1 `max_tokens` pointer, positive; type errors name the field; array content reported as unsupported.
- [x] 1.2 Unsupported fields rejected with 400 naming the field; harmless fields, `null`, `false`, `n: 1` accepted.
- [x] 1.3 `feature_tag` bound shared with `run_id`; routing-rule tags validated.
- [x] 1.4 Reservation above the token burst rejected with 400 before any charge; no negative charge.

## 2. Configuration

- [x] 2.1 Lease-size check only in lease mode; default reservation within the token burst.
- [x] 2.2 `parseFinite` at every numeric setting; `Limits.Validate` rejects non-finite values.

## 3. Docs and delivery

- [x] 3.1 `docs/gateway.md`, `docs/configuration.md`.
- [x] 3.2 Suite with `-race`, lint, `openspec validate --all`; draft PR.
