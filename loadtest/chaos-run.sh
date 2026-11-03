#!/usr/bin/env bash
# Runs the regional failover experiment against the runtime module, then
# collects the gateways' events from Cloud Logging and verifies. Knobs are
# listed in docs/operations.md.
#
# Usage: loadtest/chaos-run.sh <run_id>   (after `make runtime-apply`)
set -euo pipefail

run_id="${1:?usage: chaos-run.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/.." && pwd)"
k6_image="${K6_IMAGE:-grafana/k6:1.4.0}"

outputs="$(terraform -chdir="$root/infra/terraform/runtime" output -json)"
base_url="$(jq -r '.lb_url.value' <<<"$outputs")"
gateway_urls="$(jq -c '.gateway_urls.value' <<<"$outputs")"
admin_token="$(jq -r '.admin_token.value' <<<"$outputs")"
[[ -n "$base_url" && "$base_url" != "null" ]] || { echo "runtime module has no outputs; apply it first" >&2; exit 1; }

mkdir -p "$here/out"
echo "== run $run_id against $base_url (regions $(jq -r 'keys | join(", ")' <<<"$gateway_urls"))"
docker run --rm --user "$(id -u):$(id -g)" \
  -v "$here:/loadtest" -w /loadtest \
  -e BASE_URL="$base_url" \
  -e GATEWAY_URLS="$gateway_urls" \
  -e ADMIN_TOKEN="$admin_token" \
  -e EXPERIMENT="${CHAOS_EXPERIMENT:-region-fail}" \
  -e REGION_A="${REGION_A:-}" \
  -e CHAOS_QPS="${CHAOS_QPS:-8}" \
  -e CHAOS_STREAM_QPS="${CHAOS_STREAM_QPS:-2}" \
  -e CHAOS_SPLIT_QPS="${CHAOS_SPLIT_QPS:-15}" \
  -e CHAOS_WARMUP="${CHAOS_WARMUP:-60s}" \
  -e CHAOS_FAIL_FOR="${CHAOS_FAIL_FOR:-60s}" \
  -e CHAOS_DURATION="${CHAOS_DURATION:-300s}" \
  -e CHAOS_TIMEOUT="${CHAOS_TIMEOUT:-10s}" \
  -e RUN_ID="$run_id" \
  -e API_KEY_MAIN="${API_KEY_MAIN:-chaos-main}" \
  -e API_KEY_STREAM="${API_KEY_STREAM:-chaos-stream}" \
  -e API_KEY_SPLIT="${API_KEY_SPLIT:-chaos-split}" \
  "$k6_image" run --out "json=out/$run_id-chaos-points.json" chaos.js || k6_status=$?

# A crossed k6 threshold must not lose the run: fetch and verify anyway.
summary="$here/out/$run_id-chaos.json"
events="$here/out/$run_id-chaos-events.jsonl"
wait_s="${CHAOS_LOG_WAIT_SECONDS:-180}"
poll_s="${CHAOS_LOG_POLL_SECONDS:-10}"
expected="$(jq -n '[inputs | select(.type == "Point" and .metric == "chaos_request" and .data.tags.region != "none" and .data.tags.outcome != "not_ready")] | length' "$here/out/$run_id-chaos-points.json" 2>/dev/null || echo 0)"
experiment="$(jq -r '.summary.experiment // empty' "$summary" 2>/dev/null || true)"
region_a="$(jq -r '.summary.region_a // empty' "$summary" 2>/dev/null || true)"
collected() {
  CHAOS_MODE=cloud "$here/fetch-events.sh" "$run_id" || return 1
  jq -s -e --arg run "$run_id" --argjson n "$expected" --arg exp "$experiment" --arg A "$region_a" '
    (map(select(.msg == "cost_event" and .run_id == $run)) | length) >= $n
    and ($exp == "none" or any(.[]; .msg == "readiness_window" and .action == "down" and .region == $A))' "$events" >/dev/null
}
polls=$(( wait_s / poll_s ))
until collected; do
  if (( polls-- <= 0 )); then
    echo "Cloud Logging has fewer records than the $expected answered attempts after ${wait_s}s; verifying what arrived" >&2
    break
  fi
  sleep "$poll_s"
done
CHAOS_MODE=cloud "$here/verify-chaos.sh" "$run_id"
exit "${k6_status:-0}"
