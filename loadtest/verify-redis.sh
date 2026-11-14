#!/usr/bin/env bash
# Reads one store-outage arm and reports per tenant and period (before,
# during and after the window, by send instant); every answered request
# must match exactly one cost event by request id.
#
# Usage: loadtest/verify-redis.sh <run_id>   (run_id includes the arm)
set -euo pipefail

run_id="${1:?usage: verify-redis.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
summary="$here/out/$run_id-redis.json"
points="$here/out/$run_id-redis-points.json"
window="$here/out/$run_id-redis-window.json"
events="$here/out/$run_id-redis-events.jsonl"
for f in "$summary" "$points" "$window" "$events"; do [[ -f "$f" ]] || { echo "missing $f" >&2; exit 1; }; done

jq -e --arg run "$run_id" '.summary | .run_id == $run and (.thresholds_passed | type == "boolean")
  and (.client | keys == ["budget", "closed", "open"])
  and ([.client[] | .sent, .ok, .limited, .store_unavailable, .other_5xx, .transport, .other] | all(type == "number" and . >= 0))' \
  "$summary" >/dev/null || { echo "invalid summary for $run_id" >&2; exit 1; }
jq -e --arg run "$run_id" '.run_id == $run and (.arm == "hung" or .arm == "down")
  and ([.inject_start_ms, .inject_end_ms, .restore_start_ms, .restore_end_ms, .rpm_limit, .rpm_burst, .duration_s, .spend_counter_usd] | all(type == "number"))
  and .inject_start_ms <= .inject_end_ms and .inject_end_ms <= .restore_start_ms and .restore_start_ms <= .restore_end_ms' \
  "$window" >/dev/null || { echo "invalid injection record for $run_id" >&2; exit 1; }

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
jq -c 'select(.type == "Point" and .metric == "redis_request") | .data as $d
  | {t: ($d.tags.sent_ms // null | if . then tonumber / 1000 else null end), v: $d.value, tenant: $d.tags.tenant,
     status: ($d.tags.status | tonumber), code: ($d.tags.code // "none"), rid: ($d.tags.rid // "none")}' "$points" >"$tmp/points.jsonl"

jq -n -c --arg run "$run_id" --slurpfile p "$tmp/points.jsonl" --slurpfile e "$events" --slurpfile w "$window" --slurpfile s "$summary" '
  def pct(q): if length == 0 then null else
      sort as $s | ((length - 1) * q) as $h | ($h | floor) as $i
      | $s[$i] + ($h - $i) * ($s[[$i + 1, length - 1] | min] - $s[$i]) | . * 100 | round / 100 end;
  def r3: . * 1000 | round / 1000;
  def decision: if test("^rate limiter") then "rpm" elif test("^token limiter") then "tpm" elif test("^spend store") then "budget" else "?" end;
  def class: if .status == 200 then "ok" elif .status == 429 then "limited" elif .code == "store_unavailable" then "store_unavailable"
    elif .status >= 500 then "other_5xx" elif .status == 0 then "transport" else "other" end;
  $w[0] as $w | $s[0].summary as $sum
  | ($w.inject_start_ms / 1000) as $t0 | ($w.restore_end_ms / 1000) as $t1
  | ($e | map(select(.msg == "cost_event" and .run_id == $run))) as $ev
  | ($ev | group_by(.request_id) | map({key: .[0].request_id, value: .}) | from_entries) as $byid
  | ($p | map(. + {class: class, period: (if .t == null then null elif .t < $t0 then "before" elif .t < $t1 then "during" else "after" end)}
      | ($byid[.rid] // []) as $m
      | . + {matches: ($m | length), event: $m[0], fo: ($m[0].fail_open // "" | split(",") | map(select(. != "")))}
      | .failed = ((.fo | length) > 0 or .class == "store_unavailable"))) as $pts
  | ($pts | map(select(.rid != "none")) | map({key: .rid, value: true}) | from_entries) as $answered
  | ($e | map(select(.msg != "cost_event" and $byid[.request_id // ""] != null))) as $warn
  | ($pts | map(select(.tenant == "open"))) as $open
  | (($w.restore_end_ms - $w.inject_start_ms) / 1000) as $W
  | ($w.rpm_limit / 60) as $r
  | ($ev | map(select(.tenant == "tenant-budget" and .outcome == "success") | .cost_usd // 0) | add // 0) as $cost
  | ($warn | map(select(.msg | test("spend under-counted$")) | $byid[.request_id][0].cost_usd // 0)) as $lost
  | ($cost - ($lost | add // 0)) as $floor
  | {
      rows: [["before", "during", "after"][] as $period | ["open", "budget", "closed"][] as $tenant
        | ($pts | map(select(.period == $period and .tenant == $tenant))) as $x
        | {period: $period, tenant: $tenant, sent: ($x | length),
           ok: ($x | map(select(.class == "ok")) | length), limited: ($x | map(select(.class == "limited")) | length),
           store_unavailable: ($x | map(select(.class == "store_unavailable")) | length),
           other_5xx: ($x | map(select(.class == "other_5xx")) | length), transport: ($x | map(select(.class == "transport")) | length),
           other: ($x | map(select(.class == "other")) | length),
           latency: ($x | map(select(.status != 0) | .v) | {p50: pct(0.5), p95: pct(0.95), p99: pct(0.99)}),
           fail_open: {rpm: ($x | map(select(.fo | index("rpm"))) | length), tpm: ($x | map(select(.fo | index("tpm"))) | length),
                       budget: ($x | map(select(.fo | index("budget"))) | length)}}],
      window_s: ($W | r3), inject_s: (($w.inject_end_ms - $w.inject_start_ms) / 1000 | r3), restore_s: (($w.restore_end_ms - $w.restore_start_ms) / 1000 | r3),
      overrun: (($open | map(select(.period == "during" and .class == "ok")) | length) as $a
        | {admitted: $a, allowance: ($r * $W | r3), excess: ($a - $r * $W | r3),
           fail_open_rpm: ($open | map(select(.period == "during" and (.fo | index("rpm")))) | length),
           run_admitted: ($open | map(select(.class == "ok")) | length), run_bound: ($w.rpm_burst + $r * $w.duration_s | r3)}),
      spend: {events: ($ev | map(select(.tenant == "tenant-budget" and .outcome == "success")) | length), cost: $cost,
              lost: ($lost | length), lost_usd: ($lost | add // 0), counter: $w.spend_counter_usd, floor: $floor,
              late: ($w.spend_counter_usd - $floor)},
      recovery: {
        first_failure: ($pts | map(select(.failed and .t >= $t0)) | min_by(.t) | if . then (.t - $t0 | r3) else null end),
        deadline_failures: ($pts | map(select(.failed and .period == "during" and .v >= 250)) | length),
        last_deadline_failure: ($pts | map(select(.failed and .period == "during" and .v >= 250)) | max_by(.t) | if . then (.t - $t0 | r3) else null end),
        fast_failures: ($pts | map(select(.failed and .period == "during" and .v < 250)) | length),
        failures_after: ($pts | map(select(.failed and .period == "after")) | length),
        last_failure_after: ($pts | map(select(.failed and .period == "after")) | max_by(.t) | if . then (.t - $t1 | r3) else null end),
        first_decision_after: ($pts | map(select(.period == "after" and .status != 0 and (.failed | not))) | min_by(.t) | if . then (.t - $t1 | r3) else null end)},
      reconciliation_failures: ($warn | map(select(.msg | test("reconciliation failed"))) | length),
      failures: ([
        [ ($pts | map(select(.t == null)) | length) as $n | select($n > 0) | "\($n) k6 points without a recorded send time" ],
        [ ($pts | map(select(.rid == "none" and (.class == "ok" or .class == "limited" or .class == "store_unavailable"))) | length) as $n
          | select($n > 0) | "\($n) gateway answers (200/429/503 store_unavailable) without a request id" ],
        [ $pts[] | select(.rid != "none") | . as $x
          | if $x.matches == 0 then "request \($x.rid) (\($x.tenant), \($x.status)) has no cost event"
            elif $x.matches > 1 then "request \($x.rid) has \($x.matches) cost events"
            elif $x.event.tenant != "tenant-\($x.tenant)" then "request \($x.rid): event tenant \($x.event.tenant), client \($x.tenant)"
            elif ($x.status == 200) != ($x.event.outcome == "success") or ($x.status != 200 and ($x.event.error_code // "") != $x.code)
              then "request \($x.rid): event \($x.event.outcome)/\($x.event.error_code // ""), client \($x.status) \($x.code)"
            else empty end ][0:20],
        [ ($ev | map(select($answered[.request_id] | not)) | length) as $n | select($n > 0) | "\($n) cost events without a k6 request" ],
        [ ["open", "budget", "closed"][] as $tenant | ["sent", "ok", "limited", "store_unavailable", "other_5xx", "transport", "other"][] as $k
          | ($sum.client[$tenant][$k]) as $want | (($pts | map(select(.tenant == $tenant)) | if $k == "sent" then length else map(select(.class == $k)) | length end)) as $got
          | select($want != $got) | "\($tenant) \($k): points \($got) != summary \($want)" ],
        [ ($ev | map(.request_id as $id | .fail_open // "" | split(",")[] | select(. != "") | "\($id)/\(.)") | sort) as $field
          | ($warn | map(select(.msg | test("failing open$")) | "\(.request_id)/\(.msg | decision)") | sort) as $warned
          | select($field != $warned)
          | "fail-open decisions disagree: events \($field | map(split("/")[1]) | group_by(.) | map("\(.[0])=\(length)") | join(" ")), warnings \($warned | map(split("/")[1]) | group_by(.) | map("\(.[0])=\(length)") | join(" "))" ],
        [ ($ev | map(select(.error_code == "store_unavailable") | .request_id) | sort) as $refused
          | ($warn | map(select(.msg | test("failing closed$")) | .request_id) | sort) as $warned
          | select($refused != $warned) | "fail-closed refusals \($refused | length) != failing-closed warnings \($warned | length)" ],
        [ ($ev | map(select(.tenant == "tenant-closed" and (.fail_open // "") != "")) | length) as $n | select($n > 0)
          | "\($n) fail-closed tenant events carry fail_open" ],
        [ ($ev | map(select(.tenant != "tenant-closed" and .error_code == "store_unavailable")) | length) as $n | select($n > 0)
          | "\($n) fail-open tenant requests refused with store_unavailable" ],
        [ ($pts | map(select(.period == "before" and .failed)) | length) as $n | select($n > 0) | "\($n) store failures before the injection" ],
        [ select(($pts | map(select(.period == "during" and .failed)) | length) == 0) | "no request sent during the window met a store failure: the injection had no effect" ],
        [ select($w.spend_counter_usd < $floor - 1e-6) | "spend counter \($w.spend_counter_usd) below Σcost − Σlost = \($floor): increments lost without a warning" ],
        [ select($w.spend_counter_usd > $cost + 1e-6) | "spend counter \($w.spend_counter_usd) above Σcost \($cost)" ],
        [ select($sum.thresholds_passed != true) | "k6 thresholds failed (dropped iterations \($sum.dropped_iterations))" ]
      ] | add)
    }' >"$tmp/result.json"

res() { jq -r "$1" "$tmp/result.json"; }
echo "== run $run_id ($(jq -r .arm "$window"): $(jq -r .commands.inject "$window") → $(jq -r .commands.restore "$window"))"
echo "params:  $(jq -r '.summary.params | to_entries | map("\(.key)=\(.value)") | join(" ")' "$summary") warmup_s=$(jq -r .warmup_s "$window") fail_for_s=$(jq -r .fail_for_s "$window") rpm=$(jq -r .rpm_limit "$window") burst=$(jq -r .rpm_burst "$window")"
echo "window:  $(res .window_s) s from the injection command to the restore command's return (inject took $(res .inject_s) s, restore $(res .restore_s) s)"
echo "per period and tenant (latency over answered requests, ms):"
printf '  %-7s %-7s %6s %6s %6s %10s %9s %9s %26s %s\n' period tenant sent 200 429 503-store other-5xx transport 'p50/p95/p99' 'fail-open rpm/tpm/budget'
jq -r '.rows[] | [.period, .tenant, .sent, .ok, .limited, .store_unavailable, .other_5xx, .transport,
  (.latency | "\(.p50 // "-")/\(.p95 // "-")/\(.p99 // "-")"), (.fail_open | "\(.rpm)/\(.tpm)/\(.budget)")] | @tsv' "$tmp/result.json" \
  | while IFS=$'\t' read -r period tenant sent ok limited store server transport latency fo; do
      printf '  %-7s %-7s %6s %6s %6s %10s %9s %9s %26s %s\n' "$period" "$tenant" "$sent" "$ok" "$limited" "$store" "$server" "$transport" "$latency" "$fo"
    done
echo "overrun: open tenant admitted $(res .overrun.admitted) during the window vs r·W = $(res .overrun.allowance) → excess $(res .overrun.excess) ($(res .overrun.fail_open_rpm) fail-open rpm admissions); whole run $(res .overrun.run_admitted) admitted vs B + r·T = $(res .overrun.run_bound)"
usd() { res "$1 | . * 1e6 | round / 1e6 | if . == 0 then 0 else . end"; }
echo "spend:   $(res .spend.events) budget successes, Σcost $(usd .spend.cost) USD; lost increments (warnings) $(res .spend.lost), $(usd .spend.lost_usd) USD; store counter $(usd .spend.counter) USD = Σcost − Σlost + $(usd .spend.late) applied after the gateway reported them lost"
echo "store:   first failure +$(res '.recovery.first_failure // "none"') s after the injection; during the window $(res .recovery.deadline_failures) failures answered in 250 ms or more$(res '.recovery.last_deadline_failure | if . then " (the last sent at +\(.) s)" else "" end') and $(res .recovery.fast_failures) faster; $(res .recovery.failures_after) failures sent after the restore$(res '.recovery.last_failure_after | if . then ", the last at +\(.) s" else "" end'); first store decision after the restore at +$(res '.recovery.first_decision_after // "none"') s; token reconciliation failures $(res .reconciliation_failures)"

status=0
while IFS= read -r failure; do
  [[ -n "$failure" ]] || continue
  echo "FAIL: $failure"
  status=1
done < <(res '.failures[]')
(( status == 0 )) && echo "OK: every answered request matches one cost event; fail-open and fail-closed records agree; spend counter within bounds"
exit $status
