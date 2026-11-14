# Design: add-tool-calling

## Context

`parseCompletionRequest` decodes the request into typed fields and then scans the raw JSON for an unsupported-field list that includes every tool field. `provider.Message` has only `role` and `content`; `provider.ChatRequest` forwards model, messages, `max_tokens` and `temperature`. The stream scanner forwards each validated event byte for byte and hashes `choices[].delta.content` into the response fingerprint. The shadow namespace is a length-prefixed hash with fixed `tools=none` and `format=none` markers. See proposal.md.

## Goals / Non-Goals

**Goals:**

- The OpenAI function-calling round trip works through the gateway: tools in, tool call out (buffered and streamed), tool result in, text out.
- Every malformed tool shape is a 400 whose `param` names the field, before any charge or provider call.
- Accounting, the prompt cap, the content rules and the shadow namespace see what the provider sees.
- The mock exercises the round trip deterministically at $0.

**Non-Goals:**

- `response_format`, `strict` function definitions and other structured-output features.
- Legacy `functions`/`function_call` and the `function` role.
- Tool types other than `function` (custom, built-in) and `tool_choice` forms other than `none`/`auto`/`required`/function.
- Validating JSON Schema beyond "an object", validating that tool-call arguments are JSON, or checking that every tool call is answered: the provider owns those rules.
- Scanning responses, tool calls included, with the content rules.

## Decisions

1. **Shapes and bounds.** The limits follow OpenAI's own: at most 128 tools, names matching `^[a-zA-Z0-9_-]{1,64}$`. Names are unique, so a named `tool_choice` and a tool call resolve to one definition. The compact JSON of the `tools` array is at most 256 KiB: a quarter of the 1 MiB body, room for 128 definitions of 2 KiB, and a bound on the work the canonical hash and the estimate do. `parameters` is an object or absent (`null` counts as absent); it is forwarded with the client's key order, which models read. Fields of a tool object other than `type`, `function.name`, `function.description`, `function.parameters` and `function.strict` are ignored, like unknown top-level fields. `strict` other than `null`/`false` is rejected: it asks for structured outputs.

2. **`tool_choice` and `parallel_tool_calls` require `tools`.** OpenAI rejects both without tools; forwarding them would turn a client mistake into a provider 400. `parallel_tool_calls: false` without tools was accepted as absent before this change and is now rejected.

3. **Conversation rules.** Tool-call IDs are unique within the request. A `tool` message names, through `tool_call_id`, an ID made by an earlier assistant message and not yet answered; its `content` is a string (`null` or absent is rejected). `tool_calls` on a role other than assistant, an empty `tool_calls` array, and `tool_call_id` on a role other than tool are rejected. The order "every call answered before the next assistant turn" is left to the provider: its 400 maps to `invalid_request` without the provider's body.

4. **Parameter names.** Errors use the notation already in place: `tools[].type`, `tools[].function.name`, `tools[].function.parameters`, `tools[].function.strict`, `tool_choice`, `tool_choice.function.name`, `parallel_tool_calls`, `messages[].role`, `messages[].content`, `messages[].tool_calls`, `messages[].tool_calls[].id`, `messages[].tool_calls[].type`, `messages[].tool_calls[].function.name`, `messages[].tool_calls[].function.arguments`, `messages[].tool_call_id`. JSON type errors map their decoder path into this notation (`messages.tool_calls.function.arguments` → `messages[].tool_calls[].function.arguments`).

5. **Wire types.** `provider.Message` gains `tool_calls` and `tool_call_id`; it renders `content: null` when it has tool calls and no text, as OpenAI does, and the same type carries the buffered answer back to the client. `provider.ChatRequest` gains `tools`, `tool_choice` and `parallel_tool_calls`. `tool_choice` is normalized to its string or to `{"type":"function","function":{"name":…}}`, dropping anything else the client put in the object. Streams are not re-encoded.

6. **Accounting and the prompt cap.** One measure, `promptChars`, counts message content, tool-call names and arguments, and the compact JSON of the forwarded tool definitions. The cap compares it with `max_prompt_chars`, so a tenant cannot move text past its cap into a tool description. The estimate stays `ceil((promptChars + role bytes) / 4)`; provider usage still reconciles the charge and prices the request.

7. **Content rules.** Each family runs where it means something:

   | Text | Sensitive | Injection | Temporal |
   |---|---|---|---|
   | system messages | yes | no | no |
   | user messages | yes | yes | yes |
   | assistant content | yes | yes | no |
   | tool-call arguments | yes | yes | no |
   | tool results (`tool` messages) | yes | yes | no |
   | tool definitions (name, description, parameters) | yes | no | no |

   Tool definitions are written by the application, like the system prompt: a description that says "ignore the cached value" or "returns the current rate" is the application setting rules, so injection and temporal rules skip them, and sensitive data in them is still flagged because it still reaches the provider. Tool results carry text that came from outside the application — a web page, a document, an email — which is how indirect injection reaches a model, so they are classified for injection exactly like user content; a tenant with `injection=block` gets 403 for a poisoned tool result. Tool-call arguments carry user data the provider sees again (`send_email({"to":…})`). Temporal cues stay limited to the user's own words. The corpus and its floors are unchanged: the rules are the same, only the texts handed to them grow. Flags name kinds; no tool text is logged.

8. **Cache eligibility.** `assistant` and `tool` turns already make a request `multi_turn`; tool results are always in such a request, so they never reach the cache. A single-turn request with tools stays eligible. Its answer is often a tool call, which depends on the prompt and the tools, not on when it is asked (the time-dependent part is the tool result, in the following turn), and the shadow cache serves nothing, so measuring would-be hits on tool traffic is the point. Two conditions make it sound: the namespace covers the tools, and the fingerprint covers the tool call, so a would-be hit that calls the same function with other arguments shows as differing fingerprints.

9. **Tools in the namespace.** The `tools` component is the SHA-256 of a canonical JSON document `{"parallel_tool_calls":…,"tool_choice":…,"tools":[…]}`: tools in declared order (order is part of the prompt), every object's keys sorted, numbers as written, `tool_choice` normalized, and absent values as `null`. Reordering keys of a definition keeps the namespace; any other change of a definition, of `tool_choice` or of `parallel_tool_calls` changes it; a request without tools keys `none`. `format=none` stays while `response_format` is rejected. The rules version becomes `v3`.

10. **Fingerprints over tool calls.** `semcache.AnswerDigest` hashes the text incrementally and, per tool-call index, the function name and the arguments in two incremental hashes. A text-only answer keeps its previous fingerprint (the SHA-256 of the text); an answer with tool calls fingerprints the text hash followed by each call's two hashes in index order. Call IDs are excluded: providers make them unique per response, so including them would mark every pair as different. Fragmenting a stream does not change the hashes, so a streamed and a buffered answer fingerprint alike. A stream that uses a tool-call index outside 0–127 is not offered to the shadow cache; the per-index state is the only memory the scanner keeps per call.

11. **Mock.** With `tools` present, `tool_choice` other than `none`, and a last message that is not a tool result, the mock answers one tool call: to the function `tool_choice` names, otherwise the first tool. Its arguments are an object with one value per top-level property of the schema, keys sorted, by declared type (`"mock"` for strings, `1` for numbers and integers, `true`, `[]`, `{}`, `null` otherwise). Its ID is `call_mock_<n>`, `n` being one more than the tool calls already in the conversation, so IDs stay unique across a loop. Buffered, the message has `content: null` and `finish_reason: "tool_calls"`; streamed, the first delta carries the ID and name, the arguments follow in fragments of at most 8 bytes that never split a character, and the last chunk carries `finish_reason`. After a tool result the mock answers a fixed text. Usage stays 12 prompt and 11 completion tokens.

## Risks / Trade-offs

- A client that sent `parallel_tool_calls: false` without tools now gets 400.
- Tenants close to `max_prompt_chars` lose the room their tool definitions take.
- Arguments generated with a different key order fingerprint differently; the shadow records list them as candidates for review, never as a confirmed false positive.
- Different tool orders are different namespaces even when the set is the same.
- A provider that reports a tool-call delta with a non-string `arguments` makes that chunk invisible to the scanner, as any undecodable chunk is: forwarded, not counted.
