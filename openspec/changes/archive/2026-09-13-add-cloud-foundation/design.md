# Design: add-cloud-foundation

## Context

The GCP project already hosts other work: a shared Terraform state bucket (per-prefix isolation), a workload identity pool whose only provider is pinned to another repository, a project-wide budget, and enabled APIs. Pusaq needs its own registry, its own publishing identity, and a spending guard of its own without touching any of that. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Publishing works from the first push after apply, with nothing stored in the repository.
- Nothing bills while idle: registry storage bounded by cleanup policies is the only metered item.
- The foundation is inert to its neighbors: shared bucket by prefix, shared pool by provider, shared APIs never disabled.

**Non-Goals:**

- Deploying the gateway (Cloud Run, Memorystore, load balancing) — out of scope for this change.
- Deploy permissions for CI: the publisher can write images and nothing else.
- Multi-environment layout; one project, one foundation.

## Decisions

1. **One Terraform unit, `infra/terraform/foundation`, state prefix `pusaq/foundation`.** The bucket name is passed at `init` (`-backend-config`) rather than hardcoded, following the convention already used in the project's other Terraform. Other Terraform units use their own state prefix.

2. **A registry of its own.** Reusing the pre-existing shared repository would couple Pusaq to another unit's state and cleanup rules. Cleanup: untagged after 7 days, tagged after 30 days except the 10 most recent — a KEEP rule protects nothing unless a DELETE rule for tagged images exists next to it.

3. **A provider of its own in the existing pool.** Attribute conditions live on the provider; the pool's existing provider only accepts another repository. Adding `pusaq-github` with `assertion.repository == 'aalvaropc/pusaq'` rejects foreign tokens before IAM is consulted, and leaves the existing provider untouched. The pool name is composed from the project number instead of read through a data source, so the configuration has one fewer moving part.

4. **Least privilege for the publisher.** `roles/artifactregistry.writer` on the one repository, `roles/iam.workloadIdentityUser` for the repository's principal set, no project-level roles.

5. **Budget by label, gross spend, monthly.** `default_labels` on the provider stamps `project=pusaq` on everything this configuration creates; any module that sets the same label is counted without editing the budget. `EXCLUDE_ALL_CREDITS` measures what actually consumes credit. The existing project-wide budget stays as the outer guard; this one isolates Pusaq's share.

6. **Publishing workflow: WIF + buildx, both images, SHA and branch tags.** `google-github-actions/auth` (`token_format: access_token`) → `docker/login-action` with `oauth2accesstoken` → two `build-push-action` steps sharing the GitHub Actions layer cache. A concurrency group serializes runs so tags never race. Actions pinned by major version, matching the existing CI workflow.

7. **CI validates without credentials.** `terraform fmt -check` and `init -backend=false` + `validate` need no cloud access, so pull requests from anyone are checked and no secret is exposed to them.

## Risks / Trade-offs

- **Budget creation needs billing-account permissions** on the operator's credentials. If missing, the apply fails on that one resource and nothing else is affected.
- **Label-filtered budgets see only labeled resources.** Anything created outside Terraform without the label is invisible to it; the project-wide budget still catches it.
- **The state bucket is shared.** Prefix isolation is a convention, not an enforcement; the tradeoff is one fewer bucket to manage.
- **Cleanup policies delete tagged images after 30 days.** A tag someone relies on for longer must be re-pushed or the policy widened; deployments pin by SHA and are re-published on every push anyway.
