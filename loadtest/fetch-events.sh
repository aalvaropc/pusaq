#!/usr/bin/env bash
# Collects one chaos run's gateway records into out/<run_id>-chaos-events.jsonl:
# cost events with the run id, plus readiness_window records and fail-open
# warnings logged within the run (k6 clock ± CHAOS_CLOCK_SLACK_SECONDS).
# CHAOS_MODE=cloud reads Cloud Logging (read-only); local, the Compose logs.
#
# Usage: CHAOS_MODE=cloud|local loadtest/fetch-events.sh <run_id>
set -euo pipefail

run_id="${1:?usage: fetch-events.sh <run_id>}"
here="$(cd "$(dirname "$0")" && pwd)"
summary="$here/out/$run_id-chaos.json"
out="$here/out/$run_id-chaos-events.jsonl"
mode="${CHAOS_MODE:-cloud}"
slack="${CHAOS_CLOCK_SLACK_SECONDS:-5}"
[[ -f "$summary" ]] || { echo "missing $summary" >&2; exit 1; }
started="$(jq -r '.summary.started_at // empty' "$summary")"
ended="$(jq -r '.summary.ended_at // empty' "$summary")"
[[ -n "$started" && -n "$ended" ]] || { echo "summary has no started_at/ended_at: $summary" >&2; exit 1; }

# shellcheck disable=SC2016 # jq variables, not shell ones
select_records='
  def epoch: capture("^(?<s>[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(?<f>[.][0-9]+)?(?<z>Z|[+-][0-9]{2}:[0-9]{2})$")
    | (.s + "Z" | fromdateiso8601) + ("0" + (.f // "") | tonumber)
      - (if .z == "Z" then 0 else (if .z[0:1] == "-" then -1 else 1 end) * ((.z[1:3] | tonumber) * 3600 + (.z[4:6] | tonumber) * 60) end);
  def in_run: [.time // "" | epoch] as $t
    | ($t | length) == 1 and $t[0] >= ($started | epoch) - $slack and $t[0] <= ($ended | epoch) + $slack;
  select(.run_id == $run or ((.msg == "readiness_window" or ((.msg // "") | test("failing open"))) and in_run))'
args=(--arg run "$run_id" --arg started "$started" --arg ended "$ended" --argjson slack "$slack")

case "$mode" in
  cloud)
    project="${GCP_PROJECT:-$(gcloud config get-value project 2>/dev/null)}"
    since="$(date -u -d "$started - 1 minute" +%Y-%m-%dT%H:%M:%SZ)"
    until="$(date -u -d "$ended + 1 minute" +%Y-%m-%dT%H:%M:%SZ)"
    filter="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"pusaq-gateway\" AND timestamp>=\"$since\" AND timestamp<=\"$until\" AND (jsonPayload.run_id=\"$run_id\" OR jsonPayload.msg=\"readiness_window\" OR jsonPayload.msg:\"failing open\")"
    gcloud logging read "$filter" --project "$project" --order=asc --format=json \
      | jq -c "${args[@]}" ".[] | . as \$e | \$e.jsonPayload + {location: \$e.resource.labels.location, instance: \$e.labels.instanceId} | .time //= \$e.timestamp | $select_records" >"$out"
    ;;
  local)
    docker compose --profile loadtest logs --no-log-prefix pusaq-a pusaq-b 2>/dev/null \
      | jq -R -c "${args[@]}" "fromjson? | $select_records" >"$out"
    ;;
  *)
    echo "CHAOS_MODE must be cloud or local, got $mode" >&2; exit 1 ;;
esac

echo "events: $(wc -l <"$out") records → $out"
