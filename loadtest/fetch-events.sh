#!/usr/bin/env bash
# Collects the gateways' log records for one chaos run into
# out/<run_id>-chaos-events.jsonl (one JSON object per line): the cost
# events tagged with the run id, the readiness_window records, and any
# fail-open warnings. CHAOS_MODE=cloud reads Cloud Logging (read-only);
# CHAOS_MODE=local reads the Compose loadtest profile's container logs.
#
# Usage: CHAOS_MODE=cloud|local loadtest/fetch-events.sh <run_id>
set -euo pipefail

run_id="${1:?usage: fetch-events.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
summary="$here/out/$run_id-chaos.json"
out="$here/out/$run_id-chaos-events.jsonl"
mode="${CHAOS_MODE:-cloud}"
[[ -f "$summary" ]] || { echo "missing $summary" >&2; exit 1; }

# shellcheck disable=SC2016 # jq variables, not shell ones
select_records='select(.run_id == $run or .msg == "readiness_window" or ((.msg // "") | test("failing open")))'

case "$mode" in
  cloud)
    project="${GCP_PROJECT:-$(gcloud config get-value project 2>/dev/null)}"
    started="$(jq -r '.summary.started_at' "$summary")"
    # Records from a minute before the run started, oldest first.
    since="$(date -u -d "$started - 1 minute" +%Y-%m-%dT%H:%M:%SZ)"
    filter="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"pusaq-gateway\" AND timestamp>=\"$since\" AND (jsonPayload.run_id=\"$run_id\" OR jsonPayload.msg=\"readiness_window\" OR jsonPayload.msg:\"failing open\")"
    gcloud logging read "$filter" --project "$project" --order=asc --format=json \
      | jq -c --arg run "$run_id" ".[] | .jsonPayload + {location: .resource.labels.location, instance: .labels.instanceId} | $select_records" >"$out"
    ;;
  local)
    docker compose --profile loadtest logs --no-log-prefix pusaq-a pusaq-b 2>/dev/null \
      | jq -R -c --arg run "$run_id" "fromjson? | $select_records" >"$out"
    ;;
  *)
    echo "CHAOS_MODE must be cloud or local, got $mode" >&2; exit 1 ;;
esac

echo "events: $(wc -l <"$out") records → $out"
