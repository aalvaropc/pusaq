# Proposal: add-cost-pricing

## Why

Cost events carry tokens but not money. A tenant, a feature, a model can be compared by token counts only after someone multiplies by rates they have to look up somewhere else — and rates change, so last quarter's report stops adding up. This change makes every completion event carry its cost in dollars, computed against a price table whose version travels with the event.

## What Changes

- A price table from configuration: `PUSAQ_PRICES="<model>=<usd_per_1M_input>/<usd_per_1M_output>,..."` keyed by physical model, and `PUSAQ_PRICE_TABLE_VERSION`, an opaque label required whenever a table is set. Physical model names and rates never appear in code.
- Startup validation: with a table configured, every physical model named by a route must be priced; a version without a table, or a table without a version, is a configuration error.
- Every completion whose usage becomes known — buffered, or at stream end — carries `cost.usd` and `cost.table_version` on its cost event; the emitter renders them as `cost_usd` and `price_table_version`. Errors before the provider answered have no usage and no cost. Without a table the gateway logs one startup notice and events carry no cost fields.
- The default local environment prices the mock model so `make smoke` shows a cost.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `cost-telemetry`: completion events carry cost in dollars against a versioned price table.

## Impact

- New: `internal/pricing`.
- Modified: `internal/config` (table parsing and validation), `internal/core` (cost fields on the request and the event), `internal/gateway` (pricing finalizer, event copies), `internal/telemetry` (attrs), `cmd/pusaq` (wiring), `docker-compose.yml`, README, `docs/design.md` §5.7.
- No new dependencies.
