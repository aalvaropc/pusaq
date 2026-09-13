#!/usr/bin/env bash
# Cross-checks one load-test run from three vantage points and fails when
# they disagree: the k6 summary (client side), the mock provider's counter
# (upstream side), and the gateways' cost events (gateway side). Also reports
# Redis script calls per request and the number of fail-open admissions.
#
# Usage: loadtest/verify.sh <run_id>   (after `make loadtest` produced
# loadtest/out/<run_id>.json and the loadtest profile is still up)
set -euo pipefail

run_id="${1:?usage: verify.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
summary="$here/out/$run_id.json"
[[ -f "$summary" ]] || { echo "missing $summary" >&2; exit 1; }

compose=(docker compose --profile loadtest)

jqs() { jq -r "$1" "$summary"; }

echo "== run $run_id"
echo "params:   $(jqs '.summary.params | to_entries | map("\(.key)=\(.value)") | join(" ")')"
echo "bound:    [$(jqs '.summary.bound.lower'), $(jqs '.summary.bound.upper')] (B + r·(T ± ${EPS:-$(jqs '.summary.bound.epsilon_s')}s))"

admitted=$(jqs '.summary.exactness.admitted')
rejected=$(jqs '.summary.exactness.rejected')
other=$(jqs '.summary.exactness.other')
mock_served=$(jqs '.summary.exactness.mock_served')
dropped=$(jqs '.summary.exactness.dropped_iterations')
lower=$(jqs '.summary.bound.lower')
upper=$(jqs '.summary.bound.upper')

echo "k6:       admitted=$admitted rejected=$rejected other=$other dropped_iterations=$dropped"
echo "mock:     served(exactness)=$mock_served served(neighbor)=$(jqs '.summary.neighbor.mock_served') served(toll, both arms)=$(jqs '.summary.toll.mock_served_both_arms')"
echo "neighbor: rejected=$(jqs '.summary.neighbor.rejected') other=$(jqs '.summary.neighbor.other')"
r2='def r2: if . == null then "n/a" else (. * 100 | round / 100 | tostring) end;'
echo "toll:     limited p50/p95/p99=$(jqs "$r2"' .summary.toll.limited_ms | "\(.p50|r2)/\(.p95|r2)/\(.p99|r2)"') ms  unlimited=$(jqs "$r2"' .summary.toll.unlimited_ms | "\(.p50|r2)/\(.p95|r2)/\(.p99|r2)"') ms  p99 delta=$(jqs "$r2"' (.summary.toll.limited_ms.p99 - .summary.toll.unlimited_ms.p99) | r2') ms"

# Gateway side: cost events per replica, split by outcome. Tenant A is the
# exactness tenant; only its events are compared with k6.
echo "gateway cost events (tenant-a):"
total_events=0
for svc in pusaq-a pusaq-b; do
  line=$("${compose[@]}" logs --no-log-prefix "$svc" 2>/dev/null \
    | jq -R -s -c '
        [split("\n")[] | select(length > 0) | fromjson? | select(.msg == "cost_event" and .tenant == "tenant-a")]
        | { admitted: map(select(.error_code != "rate_limited")) | length,
            rejected: map(select(.error_code == "rate_limited")) | length }')
  a=$(jq -r '.admitted' <<<"$line"); r=$(jq -r '.rejected' <<<"$line")
  echo "  $svc: admitted=$a rejected=$r"
  total_events=$((total_events + a + r))
done
echo "  log coverage: $total_events events for $((admitted + rejected + other)) requests"

fail_open=0
for svc in pusaq-a pusaq-b; do
  n=$("${compose[@]}" logs --no-log-prefix "$svc" 2>/dev/null | grep -c "failing open" || true)
  fail_open=$((fail_open + n))
done
echo "fail-open admissions: $fail_open"

echo "redis script calls (since CONFIG RESETSTAT):"
"${compose[@]}" exec -T redis redis-cli INFO commandstats 2>/dev/null \
  | tr -d '\r' | grep -E '^cmdstat_(eval|evalsha):' | sed 's/^/  /' || echo "  (none)"
calls=$("${compose[@]}" exec -T redis redis-cli INFO commandstats 2>/dev/null \
  | tr -d '\r' | grep -E '^cmdstat_(eval|evalsha):' | sed -E 's/^cmdstat_[a-z]+:calls=([0-9]+),.*/\1/' | paste -sd+ - | bc 2>/dev/null || echo 0)
# Requests that went through the limited replicas: the overload tenant, the
# neighbor, and the limited toll arm (the unlimited arm never touches Redis).
requests_limited=$((admitted + rejected + other + $(jqs '.summary.neighbor.latency_ms.requests // 0') + $(jqs '.summary.toll.limited_ms.requests // 0')))
if [[ "$requests_limited" -gt 0 ]]; then
  echo "  script calls per limited request: $(awk -v c="$calls" -v n="$requests_limited" 'BEGIN { printf "%.3f", c / n }')"
fi

status=0
if (( admitted > upper )); then echo "FAIL: admitted $admitted > upper bound $upper"; status=1; fi
if (( admitted < lower )); then echo "FAIL: admitted $admitted < lower bound $lower"; status=1; fi
if (( mock_served != admitted )); then echo "FAIL: mock served $mock_served != k6 admitted $admitted"; status=1; fi
if (( fail_open > 0 )); then echo "FAIL: $fail_open fail-open admissions"; status=1; fi
if (( other > 0 )); then echo "FAIL: $other unexpected statuses"; status=1; fi
if [[ "$(jqs '.summary.thresholds_passed')" != "true" ]]; then echo "FAIL: k6 thresholds failed"; status=1; fi
(( status == 0 )) && echo "OK: admitted within bound, three vantage points agree"
exit $status
