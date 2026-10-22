## Context

See proposal.md. The README is the repository entry point; detailed contracts live in docs/. Runtime sources, Compose, Make targets and CI workflows determine the documentation's factual content.

## Goals / Non-Goals

Keep the README under 300 words. Give setup, API contracts, configuration, operations and measurements distinct entry points. Preserve historical run dates, environments and numerical results. Runtime and infrastructure changes are out of scope.

## Decisions

Use native Markdown, three linked badges and a compact capability table. A new docs/README.md provides navigation without duplicating reference material. Retain the numbered architecture sections because source comments and measurement reports refer to them.

Describe the authentication and payload boundary from the composed pipeline, not from obsolete HTTP-adapter descriptions. State both binary defaults and Compose overrides. Document shadow sampling's temporary prompt retention separately from the index's vectors and fingerprints. Explain CI publication from the actual job conditions.

Measurement reports remain dated evidence. A measurement index identifies current contracts and newer local evidence; corrections to report context do not replace observed values or imply a new run.

## Risks / Trade-offs

- Summary drift: verify configuration names and defaults against internal/config and resolved Compose output.
- Broken navigation: check local file targets and heading fragments, including README badges and architecture anchors.
- Misleading setup: validate shell snippets and run the documented local request against mocks when the environment permits.
- Unsupported assurances: retain explicit bounds on regional quotas, streaming failures, heuristic detection, telemetry loss and retained-data accounting.
