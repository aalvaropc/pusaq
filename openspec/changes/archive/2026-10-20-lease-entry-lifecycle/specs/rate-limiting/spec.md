## MODIFIED Requirements

### Requirement: Lease mode trades per-window exactness for fewer store round-trips

With `PUSAQ_RATELIMIT_MODE=lease`, each gateway replica SHALL take up to K request tokens (`PUSAQ_LEASE_SIZE`) from a tenant's bucket in one atomic store operation and admit up to K requests locally without consulting the store. Cumulative admissions from a full bucket SHALL never exceed the bucket capacity plus accrued refill. Within any window, the admitted count MAY fall short of strict accounting by at most `M·K` tokens across M replicas (tokens taken but not yet spent) and SHALL never exceed it. Tokens unused when a lease expires (`PUSAQ_LEASE_TTL`) or when the replica drains SHALL be returned to the shared bucket; a replica that stops without draining loses at most K tokens per tenant, which under-admits and never overruns. A store denial SHALL be cached locally until the smaller of the store-computed retry duration and the lease TTL has elapsed. Cleanup SHALL NOT detach an entry used by an admission or wait for a tenant while holding the shared tenant-map lock. Lease mode SHALL apply to request limits only; token limits SHALL remain strict. The mode SHALL be announced at startup and its configuration validated.

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
- **THEN** the replica rejects that tenant's requests locally until the smaller of the retry duration and lease TTL passes, making no further store round-trip for them

#### Scenario: Mode is explicit at startup

- **WHEN** the gateway starts with rate limiting enabled
- **THEN** the startup log names the mode, and in lease mode the lease size and TTL

#### Scenario: Lease configuration is validated

- **WHEN** the mode is neither `strict` nor `lease`, the lease size is not a positive integer, exceeds the request bucket capacity, or the lease TTL is not positive
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Admission races idle cleanup

- **WHEN** cleanup retires an idle entry after an admission captured its reference
- **THEN** the admission uses a current entry and draining leaves no unreferenced tokens

#### Scenario: Busy tenant does not block another tenant

- **WHEN** cleanup encounters a tenant whose admission is waiting on the store
- **THEN** another tenant can acquire its own entry and complete admission without waiting for that tenant

#### Scenario: Denial outlives lease TTL

- **WHEN** store retry is longer than lease TTL and tokens are returned during the local denial interval
- **THEN** the replica consults the store again after lease TTL and can use those tokens

