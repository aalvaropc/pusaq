# rate-limiting

## ADDED Requirements

### Requirement: Lease mode trades per-window exactness for fewer store round-trips

With `PUSAQ_RATELIMIT_MODE=lease`, each gateway replica SHALL take up to K request tokens (`PUSAQ_LEASE_SIZE`) from a tenant's bucket in one atomic store operation and admit up to K requests locally without consulting the store. Cumulative admissions from a full bucket SHALL never exceed the bucket capacity plus accrued refill. Within any window, the admitted count MAY fall short of strict accounting by at most `M·K` tokens across M replicas (tokens taken but not yet spent) and SHALL never exceed it. Tokens unused when a lease expires (`PUSAQ_LEASE_TTL`) or when the replica drains SHALL be returned to the shared bucket; a replica that stops without draining loses at most K tokens per tenant, which under-admits and never overruns. A store denial SHALL be cached locally until the store-computed retry time. Lease mode SHALL apply to request limits only; token limits SHALL remain strict. The mode SHALL be announced at startup and its configuration validated.

#### Scenario: Lease acquired once per K requests

- **WHEN** a tenant sends N requests to one replica in lease mode with lease size K and enough capacity
- **THEN** all N are admitted and the replica makes ⌈N/K⌉ store round-trips

#### Scenario: Cumulative admissions never exceed the bucket

- **WHEN** a tenant sends more requests than its bucket holds through any number of replicas in lease mode, with no refill accruing
- **THEN** the total admitted equals the bucket capacity and every further request is rejected

#### Scenario: Under-admission bounded by M·K

- **WHEN** demand stops while M replicas hold leases
- **THEN** the tokens not yet admitted across all replicas number at most M·K, and after every replica drains the store holds exactly the tokens that were never admitted

#### Scenario: Expired lease tokens are returned

- **WHEN** a replica's lease for a tenant passes its TTL with tokens unspent
- **THEN** those tokens are returned to the shared bucket and become available to every replica

#### Scenario: Graceful shutdown returns held tokens

- **WHEN** a replica in lease mode drains
- **THEN** every token it still holds is returned to the shared bucket before it exits

#### Scenario: Bucket with fewer than K tokens grants what it has

- **WHEN** a lease is requested from a bucket holding fewer than K but at least one whole token
- **THEN** the replica receives the whole tokens available, and the request that triggered the take is admitted

#### Scenario: Denial cached until retry

- **WHEN** the store denies a lease with a retry time
- **THEN** the replica rejects that tenant's requests locally until the retry time passes, making no further store round-trip for them

#### Scenario: Mode is explicit at startup

- **WHEN** the gateway starts with rate limiting enabled
- **THEN** the startup log names the mode, and in lease mode the lease size and TTL

#### Scenario: Lease configuration is validated

- **WHEN** the mode is neither `strict` nor `lease`, the lease size is not a positive integer, exceeds the request bucket capacity, or the lease TTL is not positive
- **THEN** the gateway fails to start with a configuration error

## MODIFIED Requirements

### Requirement: The limit is exact across replicas

In strict mode (the default), the admission decision SHALL be atomic in the shared store: under concurrent requests from any number of gateway replicas, the number admitted within a window SHALL NOT exceed the bucket capacity plus accrued refill — never a multiple of it. This property SHALL be verifiable against real gateway processes by one reproducible command whose results are published together with the protocol that produced them.

#### Scenario: Concurrent replicas cannot overrun

- **WHEN** many concurrent requests race through multiple gateway instances sharing one store, with no refill accruing
- **THEN** exactly the bucket capacity is admitted and everything beyond is rejected

#### Scenario: Exactness holds across process replicas under sustained overload

- **WHEN** two gateway processes behind a load balancer receive requests from one tenant at a multiple of its sustained rate for a duration T, starting from a full bucket of capacity B with refill rate r
- **THEN** the admitted count lies within `[B + r·(T − ε), B + r·(T + ε)]` for a small declared tolerance ε, every other request is rejected with 429 `rate_limited`, the upstream's served count equals the admitted count, and no request was admitted by fail-open

#### Scenario: A co-tenant is unaffected during another tenant's overload

- **WHEN** a second tenant sends requests well under its own limit while the first tenant is overloading its bucket
- **THEN** the second tenant receives no rejections
