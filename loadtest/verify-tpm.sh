#!/usr/bin/env bash
# Reads one token-overrun run from the gateways' cost events and computes,
# per arm: admitted, tokens charged vs actually consumed, the reservation
# error per request, the excess over the bucket bound at the end of the
# window and at its peak, and the largest number of requests in flight.
#
# Usage: loadtest/verify-tpm.sh <run_id>   (after `make tpmtest`, while the
# loadtest profile is still up)
set -euo pipefail

run_id="${1:?usage: verify-tpm.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
summary="$here/out/$run_id-tpm.json"
[[ -f "$summary" ]] || { echo "missing $summary" >&2; exit 1; }

compose=(docker compose --profile loadtest)
jqs() { jq -r "$1" "$summary"; }
logs() { "${compose[@]}" logs --no-log-prefix "$1" 2>/dev/null; }

burst=$(jqs '.summary.bound.burst'); rate=$(jqs '.summary.bound.rate_per_s')
upper=$(jqs '.summary.bound.tokens_upper'); lower=$(jqs '.summary.bound.tokens_lower')
actual=$(jqs '.summary.bound.actual_per_request'); ceiling_under=$(jqs '.summary.bound.peak_overrun_ceiling_under')

echo "== run $run_id"
echo "params: $(jqs '.summary.params | to_entries | map("\(.key)=\(.value)") | join(" ")')"
echo "bound:  nominal=$(jqs '.summary.bound.tokens_nominal') tokens, upper=$upper, lower=$lower (B=$burst, r=$rate tokens/s, actual/request=$actual, expected admitted ≈ $(jqs '.summary.bound.expected_admitted')/arm)"

events=$( { logs pusaq-a; logs pusaq-b; } | jq -R -s -c '[split("\n")[] | select(length > 0) | fromjson? | select(.msg == "cost_event")]')

# per-arm analysis over the arm's tenant; timestamps are UTC RFC3339 with a fraction
analyze() { # tenant burst rate upper
  jq -c --arg tenant "$2" --argjson B "$3" --argjson r "$4" --argjson upper "$5" '
    def ts: capture("^(?<s>[^.]+)(?:\\.(?<f>[0-9]+))?Z$") | ((.s + "Z") | fromdate) + (("0." + (.f // "0")) | tonumber);
    [.[] | select(.tenant == $tenant)] as $all
    | [$all[] | select(.error_code != "rate_limited")] | sort_by(.timestamp | ts) as $adm
    | ($adm | map(.timestamp | ts)) as $t
    | (if ($t | length) > 0 then $t[0] else 0 end) as $t0
    | ($adm | map((.usage.prompt_tokens // .prompt_tokens // 0) + (.usage.completion_tokens // .completion_tokens // 0))) as $act
    | ($adm | map(.tokens_charged // 0)) as $chg
    | [range(0; $adm | length)] as $idx
    | ($idx | map(. as $i | ($act[0:$i+1] | add) - ($B + $r * ($t[$i] - $t0)))) as $excess
    | ($idx | map(. as $i | [ $idx[] as $j | select($t[$j] <= $t[$i] and $t[$i] < $t[$j] + ($adm[$j].latency_ms / 1000)) ] | length)) as $inflight
    | { admitted: ($adm | length),
        rejected: ([$all[] | select(.error_code == "rate_limited")] | length),
        sum_charged: ($chg | add // 0),
        sum_actual: ($act | add // 0),
        error_per_request: (if ($adm | length) > 0 then (($chg | add) - ($act | add)) / ($adm | length) else 0 end),
        end_overrun: ([0, (($act | add // 0) - $upper)] | max),
        peak_overrun: ([0, ($excess | max // 0)] | max),
        max_in_flight: ($inflight | max // 0) }' <<<"$events"
}

status=0
check_arm() { # name tenant
  local name=$1 tenant=$2 a
  a=$(analyze "$name" "$tenant" "$burst" "$rate" "$upper")
  local admitted rejected sum_charged sum_actual err end_ov peak_ov inflight
  admitted=$(jq -r .admitted <<<"$a"); rejected=$(jq -r .rejected <<<"$a")
  sum_charged=$(jq -r .sum_charged <<<"$a"); sum_actual=$(jq -r .sum_actual <<<"$a")
  err=$(jq -r '.error_per_request | .*100 | round/100' <<<"$a")
  end_ov=$(jq -r '.end_overrun | .*100 | round/100' <<<"$a"); peak_ov=$(jq -r '.peak_overrun | .*100 | round/100' <<<"$a")
  inflight=$(jq -r .max_in_flight <<<"$a")
  local k6_adm mock
  k6_adm=$(jqs ".summary.arms.$name.admitted"); mock=$(jqs ".summary.arms.$name.mock_served")
  echo "$name ($tenant): admitted=$admitted (k6 $k6_adm, mock $mock) rejected=$rejected Σcharged=$sum_charged Σactual=$sum_actual error/request=$err end_overrun=$end_ov peak_overrun=$peak_ov max_in_flight=$inflight shortfall=$(awk -v n="$(jqs '.summary.bound.tokens_nominal')" -v a="$sum_actual" 'BEGIN { s = n - a; if (s < 0) s = 0; printf "%d", s }')"
  if (( admitted != k6_adm )) || (( admitted != mock )); then echo "  FAIL: admitted disagree across k6/mock/events"; status=1; fi
  if awk -v v="$end_ov" 'BEGIN{exit !(v > 0)}'; then echo "  FAIL: consumption exceeded the bound at the end of the window"; status=1; fi
  # Tokens held in reservations in flight are unavailable to new admissions:
  # an over-reserving arm may fall short of the bound by in-flight × (charged − actual).
  local charged held floor
  charged=$(jqs ".summary.arms.$name.charged_per_request")
  held=$(awk -v n="$inflight" -v c="$charged" -v a="$actual" 'BEGIN { d = c - a; if (d < 0) d = 0; printf "%d", n * d }')
  floor=$(awk -v l="$lower" -v h="$held" 'BEGIN { printf "%.2f", l - h }')
  if awk -v a="$sum_actual" -v f="$floor" 'BEGIN{exit !(a < f - 23)}'; then echo "  FAIL: consumption $sum_actual below the floor $floor (lower bound $lower minus $held held in flight)"; status=1; fi
  if [[ "$name" == "over" ]] && awk -v v="$peak_ov" 'BEGIN{exit !(v > 0)}'; then echo "  FAIL: over-reserving arm shows an overrun"; status=1; fi
  if [[ "$name" == "under" ]] && awk -v v="$peak_ov" -v c="$ceiling_under" 'BEGIN{exit !(v > c + 23)}'; then echo "  FAIL: peak overrun $peak_ov above the ceiling $ceiling_under"; status=1; fi
}
check_arm under tenant-a
check_arm over tenant-b

recon=0; open=0
for svc in pusaq-a pusaq-b; do
  recon=$((recon + $(logs "$svc" | grep -c "token reconciliation failed" || true)))
  open=$((open + $(logs "$svc" | grep -c "failing open" || true)))
done
echo "reconciliation failures: $recon   fail-open admissions: $open   dropped iterations: $(jqs '.summary.dropped_iterations')"
if (( recon > 0 )); then echo "FAIL: reconciliation failures"; status=1; fi
if (( open > 0 )); then echo "FAIL: fail-open admissions"; status=1; fi
if [[ "$(jqs '.summary.thresholds_passed')" != "true" ]]; then echo "FAIL: k6 thresholds failed"; status=1; fi
(( status == 0 )) && echo "OK: consumption within the bound; overrun bounded by the reservation error"
exit $status
