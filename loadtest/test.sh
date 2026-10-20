#!/usr/bin/env bash
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/loadtest/out" "$tmp/bin"
cp "$here/"*.sh "$tmp/loadtest/"
export FIXTURE_ROOT="$tmp"
export PATH="$tmp/bin:$PATH"
checks=0

expect() {
  local expected=$1 label=$2 result=0
  shift 2
  "$@" >"$tmp/result.log" 2>&1 || result=$?
  if (( result != expected )); then
    echo "FAIL: $label returned $result, expected $expected"
    cat "$tmp/result.log"
    exit 1
  fi
  checks=$((checks + 1))
}

cat >"$tmp/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  *"logs --no-log-prefix pusaq-a") cat "$FIXTURE_ROOT/a.jsonl" ;;
  *"logs --no-log-prefix pusaq-b") cat "$FIXTURE_ROOT/b.jsonl" ;;
  *"INFO commandstats") echo 'cmdstat_evalsha:calls=3,usec=0' ;;
  *" run "*) echo "run:$RUN_ID" >>"$FIXTURE_ROOT/stages"; exit "${K6_STATUS:-0}" ;;
  *" stop "*) echo "drain:$RUN_ID" >>"$FIXTURE_ROOT/stages"; exit "${DRAIN_STATUS:-0}" ;;
esac
SH
chmod +x "$tmp/bin/docker"

jq -n '{summary:{run_id:"fixture",params:{rpm:1},bound:{lower:2,upper:2,epsilon_s:0},
  exactness:{admitted:2,rejected:1,other:0,mock_served:2,dropped_iterations:0},
  mock_served_other:0,neighbor:{rejected:0,other:0,latency_ms:{requests:0}},
  toll:{route_reason_mismatch:0,limited_ms:{p50:0,p95:0,p99:0,requests:0},unlimited_ms:{p50:0,p95:0,p99:0}},thresholds_passed:true}}' >"$tmp/base-limiter.json"
cat >"$tmp/base-a.jsonl" <<'JSON'
{"msg":"cost_event","run_id":"fixture","tenant":"tenant-a","outcome":"success","error_code":""}
{"msg":"cost_event","run_id":"fixture","tenant":"tenant-a","outcome":"error","error_code":"rate_limited"}
{"msg":"cost_event","run_id":"unrelated","tenant":"tenant-a","outcome":"success","error_code":""}
JSON
echo '{"msg":"cost_event","run_id":"fixture","tenant":"tenant-a","outcome":"success","error_code":""}' >"$tmp/base-b.jsonl"

reset_limiter() {
  cp "$tmp/base-limiter.json" "$tmp/loadtest/out/fixture.json"
  cp "$tmp/base-a.jsonl" "$tmp/a.jsonl"
  cp "$tmp/base-b.jsonl" "$tmp/b.jsonl"
}

reset_limiter
expect 0 'consistent limiter with unrelated run ignored' bash "$tmp/loadtest/verify.sh" fixture
: >"$tmp/b.jsonl"
expect 1 'missing limiter event' bash "$tmp/loadtest/verify.sh" fixture
reset_limiter
cat "$tmp/base-b.jsonl" >>"$tmp/b.jsonl"
expect 1 'duplicate limiter event' bash "$tmp/loadtest/verify.sh" fixture
reset_limiter
sed 's/"error_code":""/"error_code":"rate_limited"/;s/"success"/"error"/' "$tmp/base-b.jsonl" >"$tmp/b.jsonl"
expect 1 'reclassified limiter event' bash "$tmp/loadtest/verify.sh" fixture
reset_limiter
jq '.summary.exactness.mock_served=1' "$tmp/base-limiter.json" >"$tmp/loadtest/out/fixture.json"
expect 1 'provider counter disagrees' bash "$tmp/loadtest/verify.sh" fixture
reset_limiter
jq '.summary.thresholds_passed=false' "$tmp/base-limiter.json" >"$tmp/loadtest/out/fixture.json"
expect 1 'limiter threshold fails' bash "$tmp/loadtest/verify.sh" fixture
reset_limiter
jq 'del(.summary.exactness.admitted)' "$tmp/base-limiter.json" >"$tmp/loadtest/out/fixture.json"
expect 1 'missing limiter counter' bash "$tmp/loadtest/verify.sh" fixture
reset_limiter
jq '.summary.run_id="another"' "$tmp/base-limiter.json" >"$tmp/loadtest/out/fixture.json"
expect 1 'wrong limiter run' bash "$tmp/loadtest/verify.sh" fixture
rm "$tmp/loadtest/out/fixture.json"
expect 1 'missing limiter summary' bash "$tmp/loadtest/verify.sh" fixture

jq -n '{summary:{run_id:"fixture",experiment:"region-fail",region_a:"a",flip_at:"2026-10-20T12:00:01.000Z",
  params:{fail_for:"2s"},client:{main_first_ok:1,main_retry_ok:1,split_admitted:2,split_rate_limited:1,
  stream_complete:2,unexpected:0},thresholds_passed:true}}' >"$tmp/base-chaos.json"
jq -nc '
  def point(arm;attempt;region;outcome;status;second):
    {type:"Point",metric:"chaos_request",data:{time:("2026-10-20T12:00:0"+second+".100Z"),value:100,
      tags:{arm:arm,attempt:attempt,region:region,outcome:outcome,status:status}}};
  point("setup";"1";"a";"ok";"200";"0"),
  point("main";"1";"a";"ok";"200";"0"),
  point("main";"2";"b";"ok";"200";"2"),
  point("split";"1";"a";"ok";"200";"2"),
  point("split";"1";"b";"ok";"200";"2"),
  point("split";"1";"b";"rate_limited";"429";"2"),
  point("stream";"1";"a";"complete";"200";"2"),
  point("stream";"1";"b";"complete";"200";"2"),
  point("main";"1";"a";"not_ready";"503";"2"),
  point("main";"1";"none";"platform";"503";"2"),
  point("stream";"1";"none";"interrupted";"0";"2")' >"$tmp/base-points.jsonl"
jq -nc '
  def event(id;tenant;region;outcome;code):
    {msg:"cost_event",run_id:"fixture",request_id:id,tenant:tenant,region:region,outcome:outcome,error_code:code};
  event("setup";"tenant-main";"a";"success";""),
  event("main1";"tenant-main";"a";"success";""),
  event("main2";"tenant-main";"b";"success";""),
  event("split1";"tenant-split";"a";"success";""),
  event("split2";"tenant-split";"b";"success";""),
  event("split3";"tenant-split";"b";"error";"rate_limited"),
  event("stream1";"tenant-stream";"a";"success";""),
  event("stream2";"tenant-stream";"b";"success";""),
  {msg:"readiness_window",action:"down",region:"a",from:"2026-10-20T12:00:01Z",until:"2026-10-20T12:00:03Z"},
  (event("unrelated";"tenant-main";"a";"success";"") | .run_id="another")' >"$tmp/base-events.jsonl"

reset_chaos() {
  cp "$tmp/base-chaos.json" "$tmp/loadtest/out/fixture-chaos.json"
  cp "$tmp/base-points.jsonl" "$tmp/loadtest/out/fixture-chaos-points.json"
  cp "$tmp/base-events.jsonl" "$tmp/loadtest/out/fixture-chaos-events.jsonl"
}

chaos() { env CHAOS_MODE="${TEST_CHAOS_MODE:-local}" bash "$tmp/loadtest/verify-chaos.sh" fixture; }

reset_chaos
expect 0 'consistent regional outcomes and non-comparable refusals' chaos
grep -q 'client outcomes without a comparable cost event' "$tmp/result.log"
for id in setup main1 main2 split1 split2 split3 stream1 stream2; do
  reset_chaos
  jq -c --arg id "$id" 'select(.request_id != $id)' "$tmp/base-events.jsonl" >"$tmp/loadtest/out/fixture-chaos-events.jsonl"
  expect 1 "missing chaos event $id" chaos
done
reset_chaos
jq -c 'select(.request_id=="split1")' "$tmp/base-events.jsonl" >>"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 1 'duplicate split event' chaos
reset_chaos
jq -c 'if .request_id=="split2" then .region="a" else . end' "$tmp/base-events.jsonl" >"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 1 'split event in wrong region' chaos
reset_chaos
jq -c 'if .request_id=="stream2" then .outcome="error" | .error_code="upstream_error" else . end' "$tmp/base-events.jsonl" >"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 1 'stream completion reclassified' chaos
reset_chaos
: >"$tmp/loadtest/out/fixture-chaos-points.json"
expect 1 'points missing despite populated summary' chaos
reset_chaos
TEST_CHAOS_MODE=cloud
jq -c 'select(.request_id != "main2")' "$tmp/base-events.jsonl" >"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 0 'cloud main tolerance retained' chaos
for _ in 1 2 3 4 5 6; do
  jq -c 'select(.request_id=="main1")' "$tmp/base-events.jsonl" >>"$tmp/loadtest/out/fixture-chaos-events.jsonl"
done
expect 1 'cloud main tolerance exceeded' chaos
reset_chaos
jq -c 'select(.request_id != "split1")' "$tmp/base-events.jsonl" >"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 1 'cloud tolerance does not hide missing split events' chaos
unset TEST_CHAOS_MODE
reset_chaos
jq 'del(.summary.client.stream_complete)' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
expect 1 'missing chaos counter' chaos
reset_chaos
rm "$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 1 'missing chaos evidence file' chaos

cat >"$tmp/loadtest/fetch-events.sh" <<'SH'
#!/usr/bin/env bash
echo "fetch:$RUN_ID" >>"$FIXTURE_ROOT/stages"
exit "${FETCH_STATUS:-0}"
SH
for name in verify verify-breaker verify-tpm verify-chaos; do
  cat >"$tmp/loadtest/$name.sh" <<'SH'
#!/usr/bin/env bash
echo "verify:$RUN_ID" >>"$FIXTURE_ROOT/stages"
exit "${VERIFY_STATUS:-0}"
SH
done
chmod +x "$tmp/loadtest/"*.sh

for kind in limiter breaker tpm chaos; do
  for scenario in '0 0 0 0 0' '7 0 0 0 7' '0 8 0 0 8' '0 0 0 9 9' '7 8 10 9 7'; do
    read -r k6_status drain_status fetch_status verify_status expected <<<"$scenario"
    : >"$tmp/stages"
    expect "$expected" "$kind stage status $scenario" env K6_STATUS="$k6_status" DRAIN_STATUS="$drain_status" \
      FETCH_STATUS="$fetch_status" VERIFY_STATUS="$verify_status" bash "$tmp/loadtest/run-measurement.sh" "$kind" fixture
    printf 'run:fixture\ndrain:fixture\n' >"$tmp/expected-stages"
    if [[ "$kind" == chaos ]]; then echo 'fetch:fixture' >>"$tmp/expected-stages"; fi
    echo 'verify:fixture' >>"$tmp/expected-stages"
    diff -u "$tmp/expected-stages" "$tmp/stages"
  done
done
: >"$tmp/stages"
expect 10 'chaos collection failure' env FETCH_STATUS=10 bash "$tmp/loadtest/run-measurement.sh" chaos fixture
grep -q '^verify:fixture$' "$tmp/stages"

for target in loadtest breakertest tpmtest chaos-local; do
  : >"$tmp/stages"
  expect 2 "$target propagates failure after verification" env K6_STATUS=7 make -s -C "$tmp" -f "$here/../Makefile" "$target" RUN_ID=fixture
  grep -q '^verify:fixture$' "$tmp/stages"
done

cat >"$tmp/bin/date" <<'SH'
#!/usr/bin/env bash
echo invocation >>"$FIXTURE_ROOT/date-calls"
printf 'fixture-%s\n' "$(wc -l <"$FIXTURE_ROOT/date-calls")"
SH
chmod +x "$tmp/bin/date"
env -u RUN_ID make -s -f "$here/../Makefile" --eval="print-run:;@printf '%s\n' '\$(RUN_ID)' '\$(RUN_ID)'" print-run >"$tmp/run-ids"
printf 'fixture-1\nfixture-1\n' >"$tmp/expected-run-ids"
diff -u "$tmp/expected-run-ids" "$tmp/run-ids"
[[ "$(wc -l <"$tmp/date-calls")" == 1 ]]
checks=$((checks + 1))
echo "OK: $checks measurement fixture checks"
