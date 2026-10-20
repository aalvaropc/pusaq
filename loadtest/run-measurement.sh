#!/usr/bin/env bash
set -euo pipefail

kind="${1:?usage: run-measurement.sh limiter|breaker|tpm|chaos <run_id>}"
run_id="${2:?usage: run-measurement.sh limiter|breaker|tpm|chaos <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
compose=(docker compose --profile loadtest)
k6=("${compose[@]}" run --rm --user "$(id -u):$(id -g)" k6)

case "$kind" in
  limiter) verifier=verify.sh ;;
  breaker) k6+=(run breaker.js); verifier=verify-breaker.sh ;;
  tpm) k6+=(run tpm.js); verifier=verify-tpm.sh ;;
  chaos) k6+=(run --out "json=out/$run_id-chaos-points.json" chaos.js); verifier=verify-chaos.sh ;;
  *) echo "unknown measurement: $kind" >&2; exit 2 ;;
esac

status=0
run_stage() {
  local result=0
  "$@" || result=$?
  if (( status == 0 )); then status=$result; fi
}

export RUN_ID="$run_id"
run_stage "${k6[@]}"
run_stage "${compose[@]}" stop pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b
if [[ "$kind" == chaos ]]; then
  run_stage env CHAOS_MODE=local "$here/fetch-events.sh" "$run_id"
fi
run_stage env CHAOS_MODE=local "$here/$verifier" "$run_id"
exit "$status"
