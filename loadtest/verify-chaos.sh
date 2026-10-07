#!/usr/bin/env bash
# Reads one regional failover run — k6's per-request points, its summary,
# and the gateways' events — and computes the numbers docs/design.md §8
# promises, each from a stated source:
#   time to B          first request sent after the flip that another region answered (k6)
#   longest gap        longest interval between successive sends that succeeded after the flip (k6)
# All k6 times are send times (completion minus duration).
#   failed / recovered first-attempt failures, and how the one retry ended (k6)
#   streams            complete / interrupted / refused (k6), cross-checked
#                      with stream_interrupted events per region
#   latency            p50/p95/p99 per region before / during / after (k6)
#   split tenant       admitted per region vs the regional caps (events, k6)
#   probe timeline     when region A's own /readyz turned 503 and back, and
#                      when the balancer first answered from B (k6 probes)
# Integrity checks fail the run: missing readiness_window records, warm-up
# share below 90 % (cloud), fail-open warnings, k6 vs events disagreement,
# unexpected statuses, k6 thresholds.
#
# Usage: CHAOS_MODE=cloud|local loadtest/verify-chaos.sh <run_id>
set -euo pipefail

run_id="${1:?usage: verify-chaos.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
summary="$here/out/$run_id-chaos.json"
points="$here/out/$run_id-chaos-points.json"
events="$here/out/$run_id-chaos-events.jsonl"
mode="${CHAOS_MODE:-cloud}"
settle="${CHAOS_SETTLE_SECONDS:-15}"
for f in "$summary" "$points" "$events"; do [[ -f "$f" ]] || { echo "missing $f" >&2; exit 1; }; done

jqs() { jq -r "$1" "$summary"; }
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

experiment="$(jqs '.summary.experiment')"
region_a="$(jqs '.summary.region_a')"
flip_at="$(jqs '.summary.flip_at // empty')"
fail_for="$(jqs '.summary.params.fail_for')"
fail_for_s="$(awk -v d="$fail_for" 'BEGIN { n = d + 0; if (d ~ /m$/) n = n * 60; printf "%d", n }')"

echo "== run $run_id ($experiment) region A = $region_a"
echo "params:  $(jqs '.summary.params | to_entries | map("\(.key)=\(.value|tostring)") | join(" ")')"
echo "client:  $(jqs '.summary.client | to_entries | map("\(.key)=\(.value)") | join(" ")')"

# 1. Compact the k6 points: one line per attempt, epoch seconds.
jq -c '
  def ts: (.[0:19] + "Z" | fromdateiso8601) + (if length > 20 then ((.[20:] | sub("Z$"; "") | sub("[+-][0-9:]+$"; "") | . + "000")[0:3] | tonumber) / 1000 else 0 end);
  select(.type == "Point" and (.metric == "chaos_request" or .metric == "chaos_probe"))
  # k6 stamps a point when the request ends; t is when it was sent
  # (end minus duration), so a request that hung for the whole client
  # timeout is placed where it entered the failover, not where it gave up.
  | {m: .metric, t: ((.data.time | ts) - (.data.value / 1000)), v: .data.value} + (.data.tags | {arm, attempt, status, region, outcome, target})
' "$points" >"$tmp/points.jsonl"

# Milliseconds matter: every "+X s" below is measured from this instant.
flip_epoch="$(if [[ -n "$flip_at" ]]; then jq -rn --arg t "$flip_at" '$t | (.[0:19] + "Z" | fromdateiso8601) + ((.[20:23] | tonumber? // 0) / 1000)'; else echo ""; fi)"

# 2. Everything k6 saw, computed in one pass.
jq -s -c --arg A "$region_a" --arg flip "$flip_epoch" --argjson fail "$fail_for_s" --argjson settle "$settle" '
  def pct(p): if length == 0 then null else (sort | .[((length - 1) * p) | floor] | . * 100 | round / 100) end;
  def stats: {n: length, p50: pct(0.5), p95: pct(0.95), p99: pct(0.99)};
  def by(f): group_by(f) | map({key: (.[0] | f), value: length}) | from_entries;
  ($flip | if . == "" then null else tonumber end) as $flip
  | (if $flip then $flip + $fail + $settle else null end) as $end
  | map(select(.m == "chaos_request")) as $req
  | map(select(.m == "chaos_probe")) as $probes
  | ($req | map(select(.arm == "main" and .attempt == "1"))) as $main1
  | ($req | map(select(.arm == "main" and .attempt == "2"))) as $main2
  | ($main1 | map(select(.outcome == "ok"))) as $ok1
  | {
      preflip_share: (if $flip then ($main1 | map(select(.t < $flip)) | (map(select(.region == $A)) | length) / (length | if . == 0 then 1 else . end)) else null end),
      time_to_b: (if $flip then ($ok1 | map(select(.t >= $flip and .region != $A)) | min_by(.t) | if . then (.t - $flip | . * 1000 | round / 1000) else null end) else null end),
      longest_gap: (if $flip then
          ($ok1 | map(select(.t >= $flip)) | sort_by(.t) | map(.t)) as $ts
          | [ $flip ] + $ts | [ range(1; length) as $i | .[$i] - .[$i - 1] ] | (max // null) | if . then (. * 1000 | round / 1000) else null end
        else null end),
      first_failure: (if $flip then ($main1 | map(select(.t >= $flip and .outcome != "ok")) | min_by(.t) | if . then (.t - $flip | . * 1000 | round / 1000) else null end) else null end),
      last_failure: (if $flip then ($main1 | map(select(.t >= $flip and .outcome != "ok")) | max_by(.t) | if . then (.t - $flip | . * 1000 | round / 1000) else null end) else null end),
      first_failures: ($main1 | map(select(.outcome != "ok")) | by("\(.outcome)/\(.status)")),
      retries: ($main2 | by(.outcome)),
      by_region: ($ok1 | by(.region)),
      setup_ok: ($req | map(select(.arm == "setup" and .outcome == "ok")) | length),
      streams: ($req | map(select(.arm == "stream")) | by("\(.outcome)@attempt\(.attempt)")),
      streams_interrupted_by_region: ($req | map(select(.arm == "stream" and .outcome == "interrupted")) | by(.region)),
      split_admitted_by_region: ($req | map(select(.arm == "split" and .outcome == "ok")) | by(.region)),
      split_limited_by_region: ($req | map(select(.arm == "split" and .outcome == "rate_limited")) | by(.region)),
      split_during_by_region: (if $flip then ($req | map(select(.arm == "split" and .outcome == "ok" and .t >= $flip and .t < $flip + $fail)) | by(.region)) else {} end),
      latency: (
        ($ok1 | group_by(.region) | map({key: .[0].region, value: (
            . as $r
            | { before: ($r | map(select($flip == null or .t < $flip)) | map(.v) | stats),
                during: (if $flip then ($r | map(select(.t >= $flip and .t < $end)) | map(.v) | stats) else null end),
                after:  (if $flip then ($r | map(select(.t >= $end)) | map(.v) | stats) else null end) }
          )}) | from_entries)),
      probe: (if $flip then {
          a_first_503: ($probes | map(select(.target == $A and .status == "503" and .t >= $flip)) | min_by(.t) | if . then (.t - $flip | . * 1000 | round / 1000) else null end),
          a_back_200: ($probes | map(select(.target == $A and .status == "503" and .t >= $flip)) | max_by(.t) | . as $last
                       | if $last then ($probes | map(select(.target == $A and .status == "200" and .t > $last.t)) | min_by(.t) | if . then (.t - $flip | . * 1000 | round / 1000) else null end) else null end),
          lb_first_from_b: ($probes | map(select(.target == "lb" and .t >= $flip and .region != $A and .region != "none")) | min_by(.t) | if . then (.t - $flip | . * 1000 | round / 1000) else null end),
          lb_non_200: ($probes | map(select(.target == "lb" and .t >= $flip and .status != "200")) | length)
        } else null end)
    }
' "$tmp/points.jsonl" >"$tmp/k6.json"

# 3. The gateways' view.
jq -s -c --arg A "$region_a" '
  def by(f): group_by(f) | map({key: (.[0] | f), value: length}) | from_entries;
  (map(select(.msg == "cost_event"))) as $ev
  | {
      events: ($ev | length),
      success_by_tenant_region: ($ev | map(select(.outcome == "success")) | by("\(.tenant)@\(.region)")),
      errors_by_code_region: ($ev | map(select(.outcome != "success")) | by("\(.error_code)@\(.region)")),
      stream_interrupted_by_region: ($ev | map(select(.tenant == "tenant-stream" and .error_code == "stream_interrupted")) | by(.region)),
      split_admitted_by_region: ($ev | map(select(.tenant == "tenant-split" and .outcome == "success")) | by(.region)),
      window_down: (map(select(.msg == "readiness_window" and .action == "down" and .region == $A)) | last | if . then {from, until} else null end),
      window_up: (map(select(.msg == "readiness_window" and .action == "up" and .region == $A)) | length),
      failing_open: (map(select((.msg // "") | test("failing open"))) | length)
    }
' "$events" >"$tmp/gw.json"

k6() { jq -r "$1" "$tmp/k6.json"; }
gw() { jq -r "$1" "$tmp/gw.json"; }
kv() { jq -r "$1 | if . == null or . == {} then \"none\" else (to_entries | map(\"\(.key)=\(.value)\") | join(\" \")) end" "$2"; }

echo "k6 success by region: $(kv '.by_region' "$tmp/k6.json")   pre-flip share of A: $(k6 '.preflip_share | if . == null then "-" else (. * 100 | round) end') %"
if [[ -n "$flip_epoch" ]]; then
  echo "failover: flip_at=$flip_at fail_for=${fail_for_s}s"
  echo "  time to B (first request sent after the flip that another region answered) = $(k6 '.time_to_b') s"
  echo "  longest interval between successful sends after the flip = $(k6 '.longest_gap') s"
  echo "  first-attempt failures after flip: first at +$(k6 '.first_failure') s, last at +$(k6 '.last_failure') s"
  echo "  probes: A /readyz first 503 at +$(k6 '.probe.a_first_503') s, back to 200 at +$(k6 '.probe.a_back_200') s; balancer first answered from another region at +$(k6 '.probe.lb_first_from_b') s; balancer /readyz non-200 probes = $(k6 '.probe.lb_non_200')"
fi
echo "main:    first-attempt failures $(kv '.first_failures' "$tmp/k6.json") → retry outcomes $(kv '.retries' "$tmp/k6.json")"
echo "streams: $(kv '.streams' "$tmp/k6.json"); interrupted by region (k6) $(kv '.streams_interrupted_by_region' "$tmp/k6.json"); stream_interrupted events by region $(kv '.stream_interrupted_by_region' "$tmp/gw.json")"
echo "split:   admitted by region (k6) $(kv '.split_admitted_by_region' "$tmp/k6.json"); (events) $(kv '.split_admitted_by_region' "$tmp/gw.json"); rate-limited by region $(kv '.split_limited_by_region' "$tmp/k6.json"); admitted during the window $(kv '.split_during_by_region' "$tmp/k6.json")"
echo "latency p50/p95/p99 ms per region:"
jq -r '.latency | to_entries[] | "  \(.key): before \(.value.before | "\(.p50)/\(.p95)/\(.p99) (n=\(.n))") during \(.value.during | if . then "\(.p50)/\(.p95)/\(.p99) (n=\(.n))" else "-" end) after \(.value.after | if . then "\(.p50)/\(.p95)/\(.p99) (n=\(.n))" else "-" end)"' "$tmp/k6.json"
echo "events:  $(gw '.events') cost events; success by tenant@region $(kv '.success_by_tenant_region' "$tmp/gw.json"); errors $(kv '.errors_by_code_region' "$tmp/gw.json"); readiness_window down=$(gw '.window_down | if . then "\(.from)→\(.until)" else "none" end') up=$(gw '.window_up'); failing-open warnings=$(gw '.failing_open')"

status=0
if [[ "$(jqs '.summary.thresholds_passed')" != "true" ]]; then echo "FAIL: k6 thresholds failed"; status=1; fi
if (( $(jqs '.summary.client.unexpected') > 0 )); then echo "FAIL: unexpected statuses (400/401/500) seen"; status=1; fi
if (( $(gw '.failing_open') > 0 )); then echo "FAIL: a gateway failed open during the run"; status=1; fi
if [[ "$experiment" != "none" ]]; then
  if [[ -z "$flip_epoch" ]]; then echo "FAIL: no flip recorded"; status=1; fi
  if [[ "$(gw '.window_down')" == "null" ]]; then echo "FAIL: no readiness_window down record for $region_a in the gateway events"; status=1; fi
  if [[ "$mode" == "cloud" ]] && awk -v s="$(k6 '.preflip_share')" 'BEGIN { exit !(s < 0.9) }'; then echo "FAIL: pre-flip share of $region_a below 90 %"; status=1; fi
fi
# k6 and the gateways must agree on what succeeded (a few events may still
# be in flight in the log pipeline; allow a handful).
# setup() sends the warm-up requests that pick region A as tenant-main too.
k6_ok=$(( $(jqs '.summary.client.main_first_ok') + $(jqs '.summary.client.main_retry_ok') + $(k6 '.setup_ok') ))
gw_ok=$(jq -r '[.success_by_tenant_region | to_entries[] | select(.key | startswith("tenant-main@")) | .value] | add // 0' "$tmp/gw.json")
if (( k6_ok - gw_ok > 5 || gw_ok - k6_ok > 5 )); then echo "FAIL: main successes disagree: k6 $k6_ok vs events $gw_ok"; status=1; fi
(( status == 0 )) && echo "OK: run consistent across k6, probes, and gateway events — the numbers above are the result"
exit $status
