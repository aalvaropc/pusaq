## 1. Implementation and verification

- [x] 1.1 Remove unnecessary prompt retention and implement index byte accounting; verify eviction, duplicate IDs and oversized entries.
- [x] 1.2 Implement nonblocking byte admission and safe evaluator shutdown; verify concurrent offers, drain and dropped/skipped counters.
- [x] 1.3 Wire positive byte-budget configuration and docs; verify isolation, race suites and lint.
