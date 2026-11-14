# Proposal: add-tool-calling

## Why

Constitution rule 7 names basic tool calling as part of the OpenAI-compatible subset, and the gateway rejects every tool field with 400: `tools`, `tool_choice`, `parallel_tool_calls`, assistant `tool_calls` and `tool` messages. An application that declares one function cannot use Pusaq by changing only its base URL.

Accepting tools touches more than the parser. Tool definitions and tool-call arguments are prompt tokens the estimate ignores and bytes the prompt cap does not see. Tool results are text the application did not write, the usual channel for indirect prompt injection. The shadow-cache namespace carries a fixed `tools=none` marker that would merge requests with different tools, and the response fingerprint hashes only text, so two different tool calls would look like the same answer.

## What Changes

- **Client-visible:** `POST /v1/chat/completions` accepts `tools` (1–128 function tools: `name` matching `^[a-zA-Z0-9_-]{1,64}$` and unique, optional `description`, optional JSON-object `parameters` forwarded as sent; at most 256 KiB of definitions), `tool_choice` (`none`, `auto`, `required`, or a function object naming a declared tool) and `parallel_tool_calls` (boolean). Both of the latter require `tools`. `strict` function definitions and tool types other than `function` are rejected naming the field.
- **Client-visible:** messages accept assistant `tool_calls` (each with a unique `id`, type `function`, a valid `function.name` and string `function.arguments`; `content` may be null or absent) and `tool` messages whose string `content` answers, through `tool_call_id`, a tool call made earlier in the conversation and not answered before. `tool_calls` on other roles, `tool_call_id` on other roles, and the legacy `function` role are rejected naming the field.
- **Client-visible:** `functions`, `function_call`, `response_format` and the rest of the unsupported list stay rejected.
- The provider request carries the tools, the normalized `tool_choice`, `parallel_tool_calls`, message tool calls and tool-call IDs. Buffered responses pass `choices[].message.tool_calls` and the provider's `finish_reason` (`tool_calls`) through, with `content` null when the answer has no text; streamed tool-call deltas are forwarded byte for byte.
- The input-token estimate and the tenant prompt cap count tool definitions and tool-call names and arguments next to message content; provider usage still settles the charge and the price.
- The policy filter scans tool definitions for sensitive data, tool-call arguments for sensitive data and instruction-override phrasing, and tool results like user content for sensitive data and instruction-override phrasing. Only kinds are recorded, never text.
- The shadow-cache namespace replaces the fixed tools marker with a hash of the canonical tool definitions, `tool_choice` and `parallel_tool_calls`, stable under JSON key order; `format=none` stays; the rules version becomes `v3`. Requests with tool turns stay `multi_turn`; single-turn requests with tools are eligible. Response fingerprints cover tool-call names and arguments, equal for buffered and streamed answers.
- The mock provider answers tools deterministically: a tool call to the forced or first tool with arguments derived from its schema, buffered or streamed in argument fragments, and a text answer after a tool result.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: the accepted subset includes basic tool calling, with validation, forwarding, response pass-through and a deterministic mock.
- `semantic-cache`: the namespace keys on the tools; tool turns are multi-turn; fingerprints cover tool calls.
- `tenant-policy`: the prompt cap counts tool definitions and tool-call arguments; the content rules cover tool definitions, tool-call arguments and tool results.

## Impact

- Code: `internal/provider/provider.go`, `internal/gateway/{completions,cache,policy,tools}.go`, `internal/semcache/shadow.go`, `internal/mockprovider/server.go`.
- Tests: `internal/gateway`, `internal/provider`, `internal/semcache`, `internal/mockprovider`.
- Docs: `docs/gateway.md`, `docs/design.md`, `docs/operations.md`, `README.md`.
- Shadow namespaces change for every request with the rules version; an index built before restarts empty anyway.
- No new configuration and no new dependencies.
