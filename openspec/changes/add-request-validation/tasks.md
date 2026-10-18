# Tasks: add-request-validation

## 1. Request validation

- [ ] 1.1 `max_tokens` pointer, positive; type errors name the field; array content reported as unsupported.
- [ ] 1.2 Unsupported fields rejected with 400 naming the field; harmless fields, `null`, `false`, `n: 1` accepted.
- [ ] 1.3 `feature_tag` bound shared with `run_id`; routing-rule tags validated.
- [ ] 1.4 Reservation above the token burst rejected with 400 before any charge; no negative charge.

## 2. Configuration

- [ ] 2.1 Lease-size check only in lease mode; default reservation within the token burst.
- [ ] 2.2 `parseFinite` at every numeric setting; `Limits.Validate` rejects non-finite values.

## 3. Docs and delivery

- [ ] 3.1 `docs/gateway.md`, `docs/configuration.md`.
- [ ] 3.2 Suite with `-race`, lint, `openspec validate --all`; draft PR.
