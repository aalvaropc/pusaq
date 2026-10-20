## Context

Measurements have separate client summaries, gateway events and provider counters. Local runs recreate their gateways and retain them for verification. Readiness refusals happen before accounting.

## Goals / Non-Goals

The reports reject inconsistent comparable evidence and preserve failures. Changing gateway telemetry or stream classification is out of scope.

## Decisions

Tag limiter requests with the run id and filter their events by that id. Classify events as success, rate-limited or unexpected instead of counting every non-429 as admitted. Chaos compares main, split and completed-stream maps by region. Platform/transport/readiness outcomes remain explicitly outside cost-event comparisons.

Keep exact local comparisons after draining gateway requests. Preserve only the existing cloud main-success tolerance; other cloud counts remain exact and incomplete log collection fails visibly.

A small command runner executes k6, collection and verification in order, remembers the first nonzero status and attempts every stage. Local Make targets share this runner, including a no-op collection stage when verification reads Compose logs directly. Freeze RUN_ID once.

Use shell fixtures with a fake Docker executable and a temporary copy of the measurement scripts. CI executes them through make test-loadtest, without Docker or new testing dependencies.

## Risks / Trade-offs

Cloud log lag can cause strict comparisons to fail; the report exposes disagreement rather than concealing missing evidence. A transport failure can be observed differently by client and gateway; only comparable outcomes are cross-checked.
