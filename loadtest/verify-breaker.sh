#!/usr/bin/env bash
# Reads one breaker run from the logs and computes, per replica, how long
# the breaker took to open after the primary started failing (detection),
# how long it stayed open, and how long after the failure ended it closed
# (recovery) — plus the requests that paid an extra attempt, the ones served
# by the fallback, and the ones that failed (must be zero).
#
# Usage: loadtest/verify-breaker.sh <run_id>   (after `make breakertest`,
# while the loadtest profile is still up)
set -euo pipefail

run_id="${1:?usage: verify-breaker.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
summary="$here/out/$run_id-breaker.json"
[[ -f "$summary" ]] || { echo "missing $summary" >&2; exit 1; }

compose=(docker compose --profile loadtest)
jqs() { jq -r "$1" "$summary"; }
epoch() { date -u -d "$1" +%s.%N; }
delta() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.3f", b - a }'; }

logs() { "${compose[@]}" logs --no-log-prefix "$1" 2>/dev/null; }

echo "== run $run_id"
echo "params:  $(jqs '.summary.params | to_entries | map("\(.key)=\(.value)") | join(" ")')"
echo "client:  200=$(jqs '.summary.client.status_200') 502=$(jqs '.summary.client.status_502') other=$(jqs '.summary.client.status_other') dropped=$(jqs '.summary.client.dropped_iterations') p50/p95/p99=$(jqs '.summary.client.latency_ms | "\(.p50|.*100|round/100)/\(.p95|.*100|round/100)/\(.p99|.*100|round/100)"') ms"

window=$(logs mock-lt | jq -R -s -c '[split("\n")[] | select(length > 0) | fromjson? | select(.msg == "failure_window" and .action == "start")] | last')
if [[ -z "$window" || "$window" == "null" ]]; then echo "FAIL: no failure_window start in mock-lt logs"; exit 1; fi
fail_from=$(jq -r '.from' <<<"$window"); fail_until=$(jq -r '.until' <<<"$window")
echo "failure: from=$fail_from until=$fail_until ($(delta "$(epoch "$fail_from")" "$(epoch "$fail_until")") s)"

status=0
for svc in pusaq-a pusaq-b; do
  transitions=$(logs "$svc" | jq -R -s -c '[split("\n")[] | select(length > 0) | fromjson? | select(.msg == "breaker_state" and .backend == "mock") | {time, from, to}]')
  n=$(jq 'length' <<<"$transitions")
  t_open=$(jq -r '[.[] | select(.to == "open")] | first | .time // empty' <<<"$transitions")
  t_closed=$(jq -r '[.[] | select(.to == "closed")] | last | .time // empty' <<<"$transitions")
  probes=$(jq '[.[] | select(.from == "half_open")] | length' <<<"$transitions")
  reopened=$(jq '[.[] | select(.from == "half_open" and .to == "open")] | length' <<<"$transitions")
  events=$(logs "$svc" | jq -R -s -c '[split("\n")[] | select(length > 0) | fromjson? | select(.msg == "cost_event")]
      | { total: length,
          extra_attempts: map(select(.attempts == 2)) | length,
          backup: map(select(.served_provider == "backup")) | length,
          upstream_errors: map(select(.error_code == "upstream_error")) | length }')
  echo "$svc: transitions=$n $(jq -r 'map("\(.from)>\(.to)") | join(" ")' <<<"$transitions")"
  if [[ -z "$t_open" ]]; then echo "  FAIL: breaker never opened"; status=1; continue; fi
  detection=$(delta "$(epoch "$fail_from")" "$(epoch "$t_open")")
  if [[ -n "$t_closed" ]]; then
    open_for=$(delta "$(epoch "$t_open")" "$(epoch "$t_closed")")
    recovery=$(delta "$(epoch "$fail_until")" "$(epoch "$t_closed")")
  else
    open_for="n/a"; recovery="n/a"
  fi
  echo "  detection=${detection}s open_for=${open_for}s recovery=${recovery}s probes=$probes reopened=$reopened"
  echo "  cost events: $(jq -r 'to_entries | map("\(.key)=\(.value)") | join(" ")' <<<"$events")"
  if awk -v d="$detection" 'BEGIN { exit !(d > 1.5) }'; then echo "  FAIL: detection $detection s > 1.5 s"; status=1; fi
  if [[ -z "$t_closed" ]]; then echo "  FAIL: breaker never closed"; status=1; fi
  if (( $(jq '.upstream_errors' <<<"$events") > 0 )); then echo "  FAIL: upstream errors reached clients"; status=1; fi
done

if (( $(jqs '.summary.client.status_502') > 0 )); then echo "FAIL: clients saw 502s"; status=1; fi
if [[ "$(jqs '.summary.thresholds_passed')" != "true" ]]; then echo "FAIL: k6 thresholds failed"; status=1; fi
(( status == 0 )) && echo "OK: breaker opened, fallback covered the outage, breaker closed after cooldown"
exit $status
