# Tasks: add-cost-pricing

## 1. Table and configuration

- [x] 1.1 `internal/pricing`: `Price`, `Table` (`NewTable`, `Cost`, `Has`, `Models`), nano-dollar rounding; tests: linear in tokens, exact rounding for the mock usage, unpriced model.
- [x] 1.2 `internal/config`: `PUSAQ_PRICES` parsing (`model=input/output`, non-negative rates, no duplicates), `PUSAQ_PRICE_TABLE_VERSION` required with a table and rejected without one, every route model must be priced; tests for each acceptance and rejection.

## 2. Events

- [x] 2.1 `internal/core`: `Cost{USD, TableVersion}` on the request and the event (`cost`, `omitzero`); `internal/gateway`: pricing finalizer registered after model resolution, cost copied into both event paths; `internal/telemetry`: `cost_usd` and `price_table_version` attrs when priced. Tests: buffered completion priced, streamed completion priced at stream end, upstream error carries no cost, no table leaves cost absent, emitter renders and omits the keys.

## 3. Wiring and delivery

- [x] 3.1 `cmd/pusaq`: build the table from config, log version and models, or the one-line notice without a table; Compose default environment prices the mock model.
- [x] 3.2 README (configuration list, cost-telemetry sentence, "Not implemented" list) and `docs/design.md` §5.7 name the variables.
- [x] 3.3 Full suite `-race` green; lint + vet clean; `make up && make smoke` shows `cost_usd` on the cost event; conventional commit(s) on `feat/cost-pricing`, draft PR to main.
