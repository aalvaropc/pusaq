## Why

Publishing requires successful verification and a narrowly scoped federated identity.

## What Changes

Use a reusable workflow called after ci-gate on push to main, scoped OIDC permissions, immutable commit tags, numeric identity conditions, and credential-free Terraform tests. Cloud deployment is out of scope.

## Capabilities

### Modified Capabilities

- `infrastructure`: Publishing requires successful verification and a narrowly scoped federated identity.

## Impact

Repository tooling and security configuration.
