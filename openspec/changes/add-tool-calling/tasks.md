# Tasks: add-tool-calling

## 1. Wire types

- [ ] 1.1 `provider`: `Message.tool_calls`/`tool_call_id` with `content: null` for a tool-call message without text; `ChatRequest.tools`/`tool_choice`/`parallel_tool_calls`; `ToolChoice` normalized on encode; buffered tool calls decoded from the HTTP adapter.

## 2. Shadow cache

- [ ] 2.1 `semcache.Key.Tools` replaces the fixed tools marker; rules version `v3`; namespace tests for tools present, absent and changed.
- [ ] 2.2 `semcache.AnswerDigest`: text and per-index tool-call name/argument hashes, call IDs excluded, bounded indices; tests for fragmentation invariance, text-only compatibility and argument sensitivity.

## 3. Gateway

- [ ] 3.1 Accept `tools`, `tool_choice`, `parallel_tool_calls`, assistant `tool_calls` and `tool` messages with the design's bounds and conversation rules; keep the legacy and structured-output fields rejected; `param` on every rule; validation table.
- [ ] 3.2 Forward the fields upstream on every attempt, retries included; pass buffered tool calls and `finish_reason` through; tool-call deltas pass the stream scanner untouched.
- [ ] 3.3 `promptChars` and the input estimate count tool definitions and tool-call names and arguments; prompt cap test.
- [ ] 3.4 Content rules over tool definitions (sensitive), tool-call arguments and tool results (sensitive, injection); tests for flags, blocking and no logged text; corpus unchanged.
- [ ] 3.5 Canonical tools hash in the namespace, stable under key order; single-turn requests with tools eligible; fingerprints over tool calls, buffered and streamed.

## 4. Mock provider

- [ ] 4.1 Deterministic tool call (forced or first tool, schema-derived arguments, unique ID), buffered and streamed in argument fragments, text after a tool result, usage on every answer; tests.

## 5. End to end, docs and verification

- [ ] 5.1 Round trip through the gateway with the HTTP adapter against the mock: tools → tool call (buffered and streamed) → tool message → text, with usage, cost and charge.
- [ ] 5.2 `docs/gateway.md`, `docs/design.md`, `docs/operations.md`, `README.md`.
- [ ] 5.3 Mutation check on the core rules; `make test`, `make lint`, `openspec validate --all`.
