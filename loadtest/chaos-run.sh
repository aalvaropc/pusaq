#!/usr/bin/env bash
# Runs the regional failover experiment against the runtime module: reads
# the balancer URL, the per-region gateway URLs and the admin token from the
# module's outputs, drives loadtest/chaos.js with k6 in Docker from this
# machine, then fetches the gateways' events from Cloud Logging and verifies.
#
# Usage: loadtest/chaos-run.sh <run_id>   (after `make runtime-apply`)
# Knobs (env): CHAOS_EXPERIMENT (region-fail | none), CHAOS_QPS,
# CHAOS_STREAM_QPS, CHAOS_SPLIT_QPS, CHAOS_WARMUP, CHAOS_FAIL_FOR,
# CHAOS_DURATION, REGION_A (override the warm-up choice), K6_IMAGE.
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
  -e RUN_ID="$run_id" \
  -e API_KEY_MAIN="${API_KEY_MAIN:-chaos-main}" \
  -e API_KEY_STREAM="${API_KEY_STREAM:-chaos-stream}" \
  -e API_KEY_SPLIT="${API_KEY_SPLIT:-chaos-split}" \
  "$k6_image" run --out "json=out/$run_id-chaos-points.json" chaos.js

# Cloud Logging lags a little behind the process; give it a moment.
sleep 30
CHAOS_MODE=cloud "$here/fetch-events.sh" "$run_id"
CHAOS_MODE=cloud "$here/verify-chaos.sh" "$run_id"
