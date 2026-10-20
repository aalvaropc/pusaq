## Why

The image build accepts the entire checkout as context, including local configuration and generated files. The binaries need only their Go module manifests and source.

## What Changes

- Admit only Dockerfile, go.mod, go.sum, cmd and internal into the build context.
- Download dependencies from both module manifests before copying source.

## Capabilities

### New Capabilities

### Modified Capabilities

No spec-level behavior changes; this is build tooling only.

## Impact

Docker build context and layer caching. Both executable targets retain their runtime contracts.
