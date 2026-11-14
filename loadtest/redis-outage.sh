#!/usr/bin/env bash
# One store-outage arm: k6 runs while Redis is paused (hung) or stopped
# (down) for the window, then drain, collection and verify-redis.sh.
#
# Usage: loadtest/redis-outage.sh <run_id> hung|down
set -euo pipefail

base="${1:?usage: redis-outage.sh <run_id> hung|down}"
arm="${2:?usage: redis-outage.sh <run_id> hung|down}"
case "$arm" in
  hung) inject=pause; restore=unpause ;;
  down) inject=stop; restore=start ;;
  *) echo "unknown arm: $arm (want hung or down)" >&2; exit 2 ;;
esac
here="$(cd "$(dirname "$0")" && pwd)"
run="$base-$arm"
warmup="${REDIS_WARMUP:-30}"
fail_for="${REDIS_FAIL_FOR:-30}"
duration="${REDIS_DURATION:-90}"
for n in "$warmup" "$fail_for" "$duration"; do
  [[ "$n" =~ ^[1-9][0-9]*$ ]] || { echo "REDIS_WARMUP, REDIS_FAIL_FOR and REDIS_DURATION are whole seconds, got $n" >&2; exit 2; }
done
(( warmup + fail_for < duration )) || { echo "the window must end before the run: $warmup + $fail_for >= $duration" >&2; exit 2; }

compose=(docker compose --profile loadtest)
mkdir -p "$here/out"
window="$here/out/$run-redis-window.json"
events="$here/out/$run-redis-events.jsonl"
now_ms() { date +%s%3N; }

status=0
keep_first() { if (( status == 0 )); then status=$1; fi; }

injected=0
restore_store() {
  if (( injected )); then
    injected=0
    "${compose[@]}" "$restore" redis
  fi
}
trap restore_store EXIT

"${compose[@]}" up -d --build --force-recreate redis pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b mock-lt mock-lt-backup lb
"${compose[@]}" exec -T redis redis-cli FLUSHALL

export RUN_ID="$run"
"${compose[@]}" run --rm --no-deps -T --user "$(id -u):$(id -g)" \
  -e RUN_ID="$run" -e REDIS_DURATION="$duration" \
  -e REDIS_OPEN_RPS="${REDIS_OPEN_RPS:-20}" -e REDIS_BUDGET_RPS="${REDIS_BUDGET_RPS:-5}" -e REDIS_CLOSED_RPS="${REDIS_CLOSED_RPS:-5}" \
  k6 run --out "json=out/$run-redis-points.json" redis-outage.js &
k6_pid=$!

sleep "$warmup"
inject_start=$(now_ms)
injected=1
inject_status=0
"${compose[@]}" "$inject" redis || inject_status=$?
inject_end=$(now_ms)
sleep "$fail_for"
restore_start=$(now_ms)
restore_status=0
restore_store || restore_status=$?
restore_end=$(now_ms)
if (( inject_status == 0 )); then inject_status=$restore_status; fi

k6_status=0
wait "$k6_pid" || k6_status=$?
status=$k6_status
if (( status == 0 )); then status=$inject_status; fi

counter=$("${compose[@]}" exec -T redis redis-cli EVAL \
  "local s = 0 for _, k in ipairs(redis.call('KEYS', ARGV[1])) do s = s + tonumber(redis.call('GET', k)) end return tostring(s)" \
  0 'spend:{tenant-budget}:*' | tr -d '\r') || keep_first $?
jq -n --arg run "$run" --arg arm "$arm" --arg inject "$inject" --arg restore "$restore" \
  --argjson inject_start "$inject_start" --argjson inject_end "$inject_end" \
  --argjson restore_start "$restore_start" --argjson restore_end "$restore_end" \
  --argjson warmup "$warmup" --argjson fail_for "$fail_for" --argjson duration "$duration" \
  --argjson rpm "${LOADTEST_RPM:-600}" --argjson burst "${LOADTEST_BURST:-100}" --arg counter "$counter" '
  {run_id: $run, arm: $arm, commands: {inject: "docker compose \($inject) redis", restore: "docker compose \($restore) redis"},
   inject_start_ms: $inject_start, inject_end_ms: $inject_end, restore_start_ms: $restore_start, restore_end_ms: $restore_end,
   warmup_s: $warmup, fail_for_s: $fail_for, duration_s: $duration, rpm_limit: $rpm, rpm_burst: $burst,
   spend_counter_usd: ($counter | tonumber? // null)}' >"$window"

"${compose[@]}" stop pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b || keep_first $?
"${compose[@]}" logs --no-log-prefix pusaq-a pusaq-b 2>/dev/null \
  | jq -R -c --arg run "$run" 'fromjson? | select((.msg == "cost_event" and .run_id == $run)
      or ((.msg // "") | test("store unavailable, failing (open|closed)$|spend under-counted$|reconciliation failed")))' >"$events" \
  || keep_first $?
echo "events: $(wc -l <"$events") records → $events"
"$here/verify-redis.sh" "$run" || keep_first $?
exit "$status"
