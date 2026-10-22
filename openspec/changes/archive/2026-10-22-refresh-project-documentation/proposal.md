## Why

The README omits the gateway's explicit development mode and security references. The documentation contains obsolete admission ordering, local commands that compete for one port, and measurement summaries that omit newer evidence.

## What Changes

- Give the README a compact introduction, CI and language badges, a capability table and a runnable local example.
- Add a documentation entry point and align architecture, API, configuration and operations with the implementation.
- Clarify authentication, I/O limits, outbound credentials, shadow retention and verified image publication.
- Index dated measurement evidence without presenting historical results as new measurements.
- Check local links, documented configuration coverage, command syntax and documentation rules.

## Capabilities

### New Capabilities

None. This is an editorial change.

### Modified Capabilities

None. Runtime contracts are unchanged; this change declares `skip_specs: true`.

## Impact

README.md and docs/ only, plus this change's artifacts. No runtime code, dependencies, configuration values, workflows or cloud resources change.
