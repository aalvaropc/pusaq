## Why

Shadow observations need bounded retained bytes and no unused raw prompts.

## What Changes

Remove raw prompts from index entries, clone retained data, clear evicted references and enforce count plus byte LRU caps. Reserve a shared pending-sample byte budget across queue and workers, return credits on every path, and synchronize shutdown with offers. Defaults are 64 MiB index and 16 MiB pending data, not total RSS guarantees.

## Capabilities

### Modified Capabilities

- `semantic-cache`: Shadow observations need bounded retained bytes and no unused raw prompts.

## Impact

Gateway behavior, configuration, and its specifications.
