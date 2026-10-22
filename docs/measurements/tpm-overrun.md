# Measurement: token-limit overrun under overload

**Claim under test.** The token limit charges a reservation on admission and
reconciles it to actual usage when the response ends. Between the two the
bucket is wrong by the reservation error, so a tenant that under-reserves
can consume more tokens than its bucket allows — but only up to a stated
ceiling, only until reconciliation catches up, and never over a whole
window. A tenant that over-reserves never exceeds the bucket, and pays for
it in throughput.

**Why it matters.** Invoices and provider quotas are counted in tokens
(docs/design.md §5.3). The limiter's exactness on tokens is what keeps a
tenant's spend where the limit says; the overrun is the honest size of the
gap the reserve-then-reconcile design leaves open.

## Hypothesis

Bucket capacity B, refill r tokens/s, window T, a fixed actual usage of
23 tokens per completion (the mock's), two tenants at the same offered
rate:

- **under**: `max_tokens: 8` → charged 2 + 8 = 10, actual 23, error −13 per
  request. Over the window Σactual ≤ B + r·(T + ε). At any instant the
  excess over `B + r·(t − t0)` is at most the burst's worth of
  under-reserved admissions, `B × (23 − 10) / 10 = 1 300` tokens at the
  defaults; reconciliations landing during the burst keep the observed peak
  below it. The bucket goes negative and repays the debt from refill before
  admitting again.
- **over**: `max_tokens: 64` → charged 66, actual 23, error +43. No excess at
  any instant. Tokens held in reservations in flight are unavailable to new
  admissions, so consumption may fall short of the nominal bound by up to
  `in-flight × 43` — the throughput cost of over-reserving.
- Both arms admit ≈ (B + r·T) / 23 requests; reconciliation never fails.

## Protocol

```
make tpmtest                        # emulated upstream latency 2 s
make tpmtest TPM_MOCK_LATENCY=0     # control: nothing in flight
make loadtest-down
```

`make tpmtest` starts the Compose `loadtest` profile with token limiting on
(`PUSAQ_TPM_LIMIT` 6000/min = 100 tokens/s, burst 1 000), the request limit
raised so it never interferes (6 000/min, burst 1 000), and the mock
upstream answering after a delay sampled in [1 s, 2 s] (`--latency-p50 2s`
with the mock's two-point model). k6 offers 20 requests/s per arm for 60 s:
tenant A on `pusaq/fast` with `max_tokens: 8`, tenant B on `pusaq/best`
with `max_tokens: 64`. `loadtest/verify-tpm.sh` reads both replicas' cost
events and computes, per arm:

1. admitted (cross-checked against k6's 200 count and the mock's per-model
   counter), rejected;
2. Σ`tokens_charged` and Σ actual usage, and the reservation error per
   request;
3. **end overrun** = max(0, Σactual − (B + r·(T + ε))) with ε = 2 s;
4. **peak overrun** = max over admitted requests, ordered by arrival, of the
   running Σactual minus `B + r·(t − t0)`;
5. **max in flight** = the largest number of admitted requests whose
   [arrival, arrival + latency) intervals overlap;
6. reconciliation failures and fail-open admissions from the logs (both
   must be zero).

It fails when consumption exceeds the bound at the end of the window, when
the over-reserving arm shows any excess, when the under-reserving arm's peak
exceeds the ceiling, when consumption falls below the lower bound minus the
tokens held in flight, or when any reconciliation failed.

## Environment

| Item | Value |
|---|---|
| Host | Linux 7.2 (Fedora), Intel Core i5-13600K (20 threads), 31 GiB; all containers on this one host |
| Docker / Compose | Docker 29.8.0, Compose v5.5.1 |
| Images | `redis:7-alpine`, `nginx:1.27-alpine`, `grafana/k6:1.4.0`, gateway and mocks built from the commit under test |
| Token limit | 6 000 tokens/min (r = 100/s), burst 1 000; reservation from `max_tokens` |
| Request limit | 6 000/min, burst 1 000 (never reached at 20 rps per arm) |
| Traffic | 2 arms × 20 rps × 60 s = 1 200 offered per arm; nominal bound 7 000 tokens, upper 7 200 |
| Mock usage | 12 prompt + 11 completion = 23 tokens per completion, fixed |

## Results

The [shared-store-clock](shared-store-clock.md#tpm-reconciliation) and
[reservation-settlement](reservation-settlement.md#local-tpm-run-2026-10-20)
reports record the 2026-10-20 runs. The values below retain their 2026-09-28
conditions.

Runs of 2026-09-28. Every run: k6 thresholds passed, `verify-tpm.sh` OK.

| Run | Latency | Arm | Charge / actual | Admitted (k6 = mock = events) | Rejected | Σcharged | Σactual | End overrun | Peak overrun | Max in flight | Shortfall vs nominal |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `tpm-2s` | [1, 2] s | under | 10 / 23 | 307 | 894 | 3 070 | 7 061 | 0 | **445** | 40 | 0 |
| `tpm-2s` | [1, 2] s | over | 66 / 23 | 289 | 912 | 19 074 | 6 647 | 0 | **0** | 16 | 353 |
| `tpm-0ms` | 0 | under | 10 / 23 | 304 | 897 | 3 040 | 6 992 | 0 | **13** | 1 | 8 |
| `tpm-0ms` | 0 | over | 66 / 23 | 302 | 899 | 19 932 | 6 946 | 0 | **0** | 1 | 54 |

Reading, control run (nothing in flight): both arms admit 302–304 requests
of 1 200 offered, within a request of the (B + r·T)/23 ≈ 304 the bucket
allows; the under-reserving arm's peak excess is exactly one request's
error (13 tokens) because every reconciliation lands before the next
arrival; the over-reserving arm never exceeds the bound.

Reading, 2 s latency: with up to 40 requests in flight
the under-reserving arm's consumption peaks 445 tokens above the bucket's
running bound — below the 520 (40 × 13) that the requests in flight could
under-reserve at once and well below the 1 300 ceiling stated up front —
and ends the window at 7 061 tokens, 61 over the nominal 7 000 and inside
the 7 200 the refill during ε allows. The bucket absorbs the debt: the arm
admits 307 requests, three more than the control run, and nothing else.
The over-reserving arm never exceeds the bound and pays with throughput:
up to 16 requests hold 43 surplus tokens each until their refund lands, so
it admits 289 requests and consumes 353 tokens fewer than the nominal
bound.

Reconciliation failures 0, fail-open 0, dropped iterations 0 in every run.

## Caveats

- **Fixed usage on the mock.** Real completions vary in length; the
  reservation error is then a distribution, and the peak excess scales with
  how far below actual the reservation sits for the requests admitted
  during the burst.
- **Emulated latency.** [1, 2] s is a plausible LLM response time; longer
  responses hold reservations longer, which enlarges both the
  under-reserving arm's peak and the over-reserving arm's shortfall.
- **The bucket has no floor.** Reconciliation can drive it negative; the
  debt is repaid from refill before anything is admitted, which is what
  keeps the window total within the bound. A very large under-reservation
  would translate into a long denial period, not into unbounded
  consumption.
- **One host, one clock; millisecond timestamps** in the events bound the
  arithmetic error to a fraction of a token.
