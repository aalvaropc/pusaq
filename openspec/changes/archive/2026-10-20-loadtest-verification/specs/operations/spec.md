## ADDED Requirements

### Requirement: Failed measurements retain verification

Local load, breaker, TPM and chaos commands SHALL attempt their evidence collection and verification after k6 finishes, including when k6 fails. They SHALL use one stable run id. A k6 failure SHALL retain its exit code; otherwise the first collection or verification failure SHALL determine the exit status.

#### Scenario: Client threshold fails

- **WHEN** k6 exits with a failing threshold
- **THEN** evidence collection and verification are attempted and the command exits with the k6 failure

#### Scenario: Evidence collection fails

- **WHEN** k6 succeeds but evidence collection fails
- **THEN** verification is attempted and the command exits with the collection failure

#### Scenario: Verifier detects inconsistent evidence

- **WHEN** execution and collection succeed but verification fails
- **THEN** the command exits with the verification failure
