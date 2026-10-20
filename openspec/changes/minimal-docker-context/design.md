## Context

Both targets compile from cmd and internal. There are no embedded files outside those directories.

## Goals / Non-Goals

The context contains only build inputs. Runtime image contents and Go dependency versions are unchanged.

## Decisions

Use an allowlist instead of enumerating sensitive filenames: unrecognized root paths are excluded automatically. Copy both module manifests together so dependency caching follows the checksum file as well as go.mod.

## Risks / Trade-offs

Build inputs outside cmd and internal require an explicit context rule. Exporting the selected context provides a direct check of the included files.
