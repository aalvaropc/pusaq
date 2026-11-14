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

logged() {
  grep -q -- "$1" "$tmp/result.log" && return
  echo "FAIL: output lacks '$1'"
  cat "$tmp/result.log"
  exit 1
}

cat >"$tmp/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  *"logs --no-log-prefix pusaq-a pusaq-b") cat "$FIXTURE_ROOT/a.jsonl" "$FIXTURE_ROOT/b.jsonl" ;;
  *"logs --no-log-prefix pusaq-a") cat "$FIXTURE_ROOT/a.jsonl" ;;
  *"logs --no-log-prefix pusaq-b") cat "$FIXTURE_ROOT/b.jsonl" ;;
  *"logs --no-log-prefix mock-lt") cat "$FIXTURE_ROOT/mock-lt.jsonl" ;;
  *"INFO commandstats") echo 'cmdstat_evalsha:calls=3,usec=0' ;;
  *" up "*) echo "up:${LOADTEST_TENANT_POLICIES:-}" >>"$FIXTURE_ROOT/stages"; exit "${UP_STATUS:-0}" ;;
  *"redis-cli FLUSHALL") echo flush >>"$FIXTURE_ROOT/stages" ;;
  *"redis-cli EVAL "*) echo "${SPEND_COUNTER:-0.5}" ;;
  *" pause redis") echo inject:pause >>"$FIXTURE_ROOT/stages"; exit "${INJECT_STATUS:-0}" ;;
  *" stop redis") echo inject:stop >>"$FIXTURE_ROOT/stages"; exit "${INJECT_STATUS:-0}" ;;
  *" unpause redis") echo restore:unpause >>"$FIXTURE_ROOT/stages"; exit "${RESTORE_STATUS:-0}" ;;
  *" start redis") echo restore:start >>"$FIXTURE_ROOT/stages"; exit "${RESTORE_STATUS:-0}" ;;
  *" run "*) echo "run:$RUN_ID" >>"$FIXTURE_ROOT/stages"; exit "${K6_STATUS:-0}" ;;
  *" stop "*) echo "drain:$RUN_ID" >>"$FIXTURE_ROOT/stages"; exit "${DRAIN_STATUS:-0}" ;;
esac
SH
cat >"$tmp/bin/terraform" <<'SH'
#!/usr/bin/env bash
echo '{"lb_url":{"value":"http://lb"},"gateway_urls":{"value":{"a":"http://a","b":"http://b"}},"admin_token":{"value":"token"}}'
SH
cat >"$tmp/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1 $2" == "logging read" ]] || exit 2
echo read >>"$FIXTURE_ROOT/gcloud-reads"
if (( $(wc -l <"$FIXTURE_ROOT/gcloud-reads") <= ${GCLOUD_LAG:-0} )); then cat "$FIXTURE_ROOT/gcloud-partial.json"; else cat "$FIXTURE_ROOT/gcloud-full.json"; fi
SH
cat >"$tmp/bin/sleep" <<'SH'
#!/usr/bin/env bash
echo "$1" >>"$FIXTURE_ROOT/sleeps"
[[ "$1" != "${SLEEP_FAIL_ON:-}" ]]
SH
chmod +x "$tmp/bin/"*

command -v node >/dev/null || { echo "FAIL: the chaos client checks require Node.js"; exit 1; }
cp "$here/chaos-lib.js" "$tmp/chaos-lib.mjs"
js_equals() {
  local label=$1 want=$2 expr=$3
  expect 0 "$label" node --input-type=module -e "
    import { durationSeconds, retryStream, streamOutcome } from '$tmp/chaos-lib.mjs';
    let got;
    try { got = String($expr); } catch (e) { got = 'throws'; }
    if (got !== '$want') { console.log('got ' + got + ', want $want'); process.exit(1); }"
}
js_equals 'stream ending in [DONE] completes' complete "streamOutcome({status: 200, body: 'data: {}\n\ndata: [DONE]\n\n', timings: {receiving: 5}}, 'ok')"
js_equals 'stream without [DONE] is interrupted' interrupted "streamOutcome({status: 200, body: 'data: {}\n\n', timings: {receiving: 5}}, 'ok')"
js_equals 'transport failure after the first byte is interrupted' interrupted "streamOutcome({status: 0, body: 'data: x\n\n', timings: {receiving: 300}}, 'transport')"
js_equals 'transport failure before any byte never started' never_started "streamOutcome({status: 0, body: null, timings: {receiving: 0}}, 'transport')"
js_equals 'refusal before streaming' refused "streamOutcome({status: 503, body: '{}', timings: {receiving: 1}}, 'not_ready')"
js_equals 'gateway error on a stream' error "streamOutcome({status: 400, body: '{}', timings: {receiving: 1}}, 'error')"
js_equals 'never-started stream is retried' true "retryStream('never_started', 'transport')"
js_equals 'interrupted stream is not retried' false "retryStream('interrupted', 'transport')"
js_equals 'platform refusal is retried' true "retryStream('refused', 'platform')"
js_equals 'rate-limited stream is not retried' false "retryStream('refused', 'rate_limited')"
for duration in '10s 10' '1m30s 90' '1500ms 1.5' '1h 3600' '2.5s 2.5' '.5s 0.5' '0 0' '10 throws' '1d throws' '10S throws'; do
  read -r text want <<<"$duration"
  js_equals "client duration $text" "$want" "durationSeconds('$text')"
done

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

jq -n '{summary:{run_id:"fixture",experiment:"region-fail",region_a:"a",started_at:"2026-10-20T12:00:00.000Z",
  flip_at:"2026-10-20T12:00:01.000Z",ended_at:"2026-10-20T12:00:10.000Z",
  params:{fail_for:"2s"},client:{main_first_ok:1,main_retry_ok:1,split_admitted:2,split_rate_limited:1,
  stream_complete:2,stream_interrupted:0,stream_never_started:1,stream_refused:0,unexpected:0},thresholds_passed:true}}' >"$tmp/base-chaos.json"
jq -nc '
  def point(arm;attempt;region;outcome;status;second):
    {type:"Point",metric:"chaos_request",data:{time:("2026-10-20T12:00:0"+second+".100Z"),value:100,
      tags:{arm:arm,attempt:attempt,region:region,outcome:outcome,status:status,
        sent_ms:(("2026-10-20T12:00:0"+second+"Z" | fromdateiso8601) * 1000 | tostring)}}};
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
  point("stream";"1";"none";"never_started";"0";"2"),
  point("stream";"2";"none";"never_started";"0";"3")' >"$tmp/base-points.jsonl"
jq -nc '
  def event(id;tenant;region;outcome;code):
    {time:"2026-10-20T12:00:02.500Z",msg:"cost_event",run_id:"fixture",request_id:id,tenant:tenant,region:region,outcome:outcome,error_code:code};
  event("setup";"tenant-main";"a";"success";""),
  event("main1";"tenant-main";"a";"success";""),
  event("main2";"tenant-main";"b";"success";""),
  event("split1";"tenant-split";"a";"success";""),
  event("split2";"tenant-split";"b";"success";""),
  event("split3";"tenant-split";"b";"error";"rate_limited"),
  event("stream1";"tenant-stream";"a";"success";""),
  event("stream2";"tenant-stream";"b";"success";""),
  {time:"2026-10-20T12:00:01.050Z",msg:"readiness_window",action:"down",region:"a",from:"2026-10-20T12:00:01.05Z",until:"2026-10-20T12:00:03.05Z"},
  (event("unrelated";"tenant-main";"a";"success";"") | .run_id="another")' >"$tmp/base-events.jsonl"

reset_chaos() {
  cp "$tmp/base-chaos.json" "$tmp/loadtest/out/fixture-chaos.json"
  cp "$tmp/base-points.jsonl" "$tmp/loadtest/out/fixture-chaos-points.json"
  cp "$tmp/base-events.jsonl" "$tmp/loadtest/out/fixture-chaos-events.jsonl"
}

chaos() { env CHAOS_MODE="${TEST_CHAOS_MODE:-local}" bash "$tmp/loadtest/verify-chaos.sh" fixture; }

reset_chaos
expect 0 'consistent regional outcomes and non-comparable refusals' chaos
logged 'client outcomes without a comparable cost event'
logged 'streams: final complete=2 interrupted=0 never_started=1 refused=0'
logged 'complete@attempt1=2 never_started@attempt1=1 never_started@attempt2=1;'
reset_chaos
jq '.summary.client.stream_interrupted=1' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
expect 1 'interrupted streams disagree with points' chaos
reset_chaos
jq '.summary.client.stream_never_started=0' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
expect 1 'never-started streams disagree with points' chaos
reset_chaos
jq '.summary.client.main_first_ok=2' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
jq -nc '{type:"Point",metric:"chaos_request",data:{time:"2026-10-20T12:00:05.100Z",value:100,tags:{arm:"main",attempt:"1",
  region:"b",outcome:"ok",status:"200",sent_ms:(("2026-10-20T12:00:01Z" | fromdateiso8601) * 1000 + 600 | tostring)}}}' \
  >>"$tmp/loadtest/out/fixture-chaos-points.json"
echo '{"msg":"cost_event","run_id":"fixture","request_id":"main3","tenant":"tenant-main","region":"b","outcome":"success","error_code":""}' \
  >>"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 0 'times come from the recorded send instant' chaos
logged 'another region answered) = 0.6 s'
reset_chaos
jq -c 'if .data.tags.outcome == "rate_limited" then del(.data.tags.sent_ms) else . end' "$tmp/base-points.jsonl" >"$tmp/loadtest/out/fixture-chaos-points.json"
expect 1 'point without a recorded send time' chaos
reset_chaos
jq '.summary.client.main_first_ok=4' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
for value in 200 300 400; do
  jq -nc --argjson v "$value" '{type:"Point",metric:"chaos_request",data:{time:"2026-10-20T12:00:00.500Z",value:$v,tags:{arm:"main",
    attempt:"1",region:"a",outcome:"ok",status:"200",sent_ms:(("2026-10-20T12:00:00Z" | fromdateiso8601) * 1000 | tostring)}}}' \
    >>"$tmp/loadtest/out/fixture-chaos-points.json"
  jq -nc --arg id "main-$value" '{msg:"cost_event",run_id:"fixture",request_id:$id,tenant:"tenant-main",region:"a",outcome:"success",error_code:""}' \
    >>"$tmp/loadtest/out/fixture-chaos-events.jsonl"
done
expect 0 'percentiles interpolate between closest ranks' chaos
logged 'a: before 250/385/397 (n=4)'
for duration in '1m30s 90' '1500ms 1.5' '1h 3600' '2.5s 2.5'; do
  read -r text seconds <<<"$duration"
  reset_chaos
  jq --arg d "$text" '.summary.params.fail_for=$d' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
  expect 0 "failure window $text" chaos
  logged "fail_for=${seconds}s"
done
reset_chaos
jq '.summary.params.fail_for="90"' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
expect 1 'failure window without a unit' chaos
reset_chaos
jq -c 'select(.msg == "readiness_window")' "$tmp/base-events.jsonl" >>"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 1 'second readiness window in one run' chaos
reset_chaos
jq '.summary.experiment="none" | del(.summary.flip_at)' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
jq -c 'select(.msg != "readiness_window")' "$tmp/base-events.jsonl" >"$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 0 'control run without a readiness window' chaos
cp "$tmp/base-events.jsonl" "$tmp/loadtest/out/fixture-chaos-events.jsonl"
expect 1 'control run with a readiness window' chaos
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

fetch() { env CHAOS_MODE=local bash "$tmp/loadtest/fetch-events.sh" fixture; }
reset_chaos
{
  cat "$tmp/base-events.jsonl"
  echo '{"time":"2026-10-20T11:50:00Z","msg":"readiness_window","action":"down","region":"a","from":"2026-10-20T11:50:00Z","until":"2026-10-20T11:51:00Z"}'
  echo '{"time":"2026-10-20T07:00:02.5-05:00","msg":"rate limiter store unavailable, failing open"}'
  echo '{"time":"2026-10-20T12:00:02.5+05:00","msg":"spend store unavailable, failing open"}'
  echo 'not json'
} >"$tmp/a.jsonl"
: >"$tmp/b.jsonl"
expect 0 'local collection keeps only this run' fetch
jq -s -e '(map(select(.msg == "cost_event" and .run_id == "fixture")) | length) == 8 and length == 10
  and (map(select(.msg == "readiness_window")) | .[0].from) == "2026-10-20T12:00:01.05Z"
  and (map(select((.msg // "") | test("failing open"))) | .[0].msg) == "rate limiter store unavailable, failing open"' \
  "$tmp/loadtest/out/fixture-chaos-events.jsonl" >/dev/null || { echo "FAIL: collection kept records outside the run"; exit 1; }
reset_chaos
{
  cat "$tmp/base-events.jsonl"
  echo '{"time":"2026-10-20T11:50:00Z","msg":"readiness_window","action":"down","region":"a","from":"2026-10-20T11:50:00Z","until":"2026-10-20T11:51:00Z"}'
} >"$tmp/a.jsonl"
expect 0 'readiness window of an earlier run ignored' fetch
expect 0 'verified after collection' chaos
reset_chaos
jq 'del(.summary.ended_at)' "$tmp/base-chaos.json" >"$tmp/loadtest/out/fixture-chaos.json"
expect 1 'collection without the run interval' fetch

jq -s '[.[] | {timestamp: .time, jsonPayload: del(.time), resource: {labels: {location: (.region // "")}}, labels: {instanceId: "i"}}]' \
  "$tmp/base-events.jsonl" >"$tmp/gcloud-full.json"
jq '.[0:4]' "$tmp/gcloud-full.json" >"$tmp/gcloud-partial.json"
cloud_run() {
  rm -f "$tmp/gcloud-reads" "$tmp/sleeps"
  touch "$tmp/gcloud-reads" "$tmp/sleeps"
  reset_chaos
  env GCP_PROJECT=fixture RUN_ID=fixture "$@" bash "$tmp/loadtest/chaos-run.sh" fixture
}
expect 0 'cloud collection waits for lagging logs' cloud_run GCLOUD_LAG=2
[[ "$(wc -l <"$tmp/gcloud-reads") $(wc -l <"$tmp/sleeps")" == "3 2" ]] || { echo "FAIL: expected three reads and two waits"; exit 1; }
expect 1 'cloud collection stops at its bound' cloud_run GCLOUD_LAG=99 CHAOS_LOG_WAIT_SECONDS=30
[[ "$(wc -l <"$tmp/gcloud-reads") $(wc -l <"$tmp/sleeps")" == "4 3" ]] || { echo "FAIL: expected four reads and three waits"; exit 1; }
logged 'fewer records than the 8 answered attempts after 30s'
expect 7 'cloud run keeps the k6 failure' cloud_run K6_STATUS=7

jq -n '{summary:{params:{rps:8},client:{status_200:10,status_502:0,status_other:0,dropped_iterations:0,
  latency_ms:{p50:1,p95:2,p99:3}},thresholds_passed:true}}' >"$tmp/loadtest/out/fixture-breaker.json"
echo '{"msg":"failure_window","action":"start","from":"2026-10-20T12:00:00Z","until":"2026-10-20T12:00:15Z"}' >"$tmp/mock-lt.jsonl"
jq -nc '
  def state(at;from;to): {time:("2026-10-20T12:00:"+at+"Z"),msg:"breaker_state",backend:"mock",from:from,to:to};
  state("00.876";"closed";"open"), state("10.900";"open";"half_open"), state("10.902";"half_open";"open"),
  state("20.950";"open";"half_open"), state("20.952";"half_open";"closed"),
  {msg:"cost_event",attempts:2,served_provider:"backup",error_code:""}' >"$tmp/a.jsonl"
cp "$tmp/a.jsonl" "$tmp/b.jsonl"
expect 0 'breaker run counts half-open episodes' bash "$tmp/loadtest/verify-breaker.sh" fixture
logged 'half_open_episodes=2 reopened=1'

t0=$(jq -n '"2026-10-20T12:00:10Z" | fromdateiso8601 * 1000')
jq -n --argjson t0 "$t0" '{run_id:"fixture-hung",arm:"hung",commands:{inject:"docker compose pause redis",restore:"docker compose unpause redis"},
  inject_start_ms:$t0,inject_end_ms:($t0 + 100),restore_start_ms:($t0 + 10000),restore_end_ms:($t0 + 10200),
  warmup_s:10,fail_for_s:10,duration_s:30,rpm_limit:60,rpm_burst:2,spend_counter_usd:0.068}' >"$tmp/base-redis-window.json"
jq -n '{summary:{run_id:"fixture-hung",params:{duration_s:30},dropped_iterations:0,thresholds_passed:true,client:{
  open:{sent:7,ok:5,limited:1,store_unavailable:0,other_5xx:0,transport:1,other:0},
  budget:{sent:3,ok:3,limited:0,store_unavailable:0,other_5xx:0,transport:0,other:0},
  closed:{sent:3,ok:2,limited:0,store_unavailable:1,other_5xx:0,transport:0,other:0}}}}' >"$tmp/base-redis.json"
jq -nc --argjson t0 "$t0" '
  def point(tenant; status; code; rid; at; ms): {type:"Point",metric:"redis_request",data:{time:"2026-10-20T12:00:30Z",value:ms,
    tags:{tenant:tenant,status:(status | tostring),code:code,rid:rid,sent_ms:($t0 + at * 1000 | tostring)}}};
  {type:"Metric",metric:"redis_request",data:{name:"redis_request"}},
  {type:"Point",metric:"http_req_duration",data:{time:"2026-10-20T12:00:30Z",value:1,tags:{}}},
  point("open";200;"none";"o1";-5;2), point("open";429;"rate_limited";"o2";-5;1),
  point("budget";200;"none";"b1";-5;3), point("closed";200;"none";"c1";-5;2),
  point("open";200;"none";"o3";2;500), point("open";200;"none";"o4";3;510),
  point("budget";200;"none";"b2";2;1000), point("closed";503;"store_unavailable";"c2";2;250),
  point("open";200;"none";"o5";10.5;300), point("open";200;"none";"o6";11;2), point("open";0;"none";"none";11.5;10000),
  point("budget";200;"none";"b3";12;3), point("closed";200;"none";"c3";12;2)' >"$tmp/base-redis-points.json"
jq -nc '
  def event(id; tenant; outcome; code; fo; cost): {msg:"cost_event",run_id:"fixture-hung",request_id:id,tenant:tenant,outcome:outcome,error_code:code}
    + (if fo == "" then {} else {fail_open:fo} end) + (if cost > 0 then {cost_usd:cost} else {} end);
  def warn(id; msg): {msg:msg,request_id:id};
  event("o1";"tenant-open";"success";"";"";0), event("o2";"tenant-open";"error";"rate_limited";"";0),
  event("b1";"tenant-budget";"success";"";"";0.034), event("c1";"tenant-closed";"success";"";"";0),
  event("o3";"tenant-open";"success";"";"rpm,tpm";0), event("o4";"tenant-open";"success";"";"rpm,tpm";0),
  event("b2";"tenant-budget";"success";"";"rpm,budget,tpm";0.034), event("c2";"tenant-closed";"error";"store_unavailable";"";0),
  event("o5";"tenant-open";"success";"";"rpm";0), event("o6";"tenant-open";"success";"";"";0),
  event("b3";"tenant-budget";"success";"";"";0.034), event("c3";"tenant-closed";"success";"";"";0),
  (event("other";"tenant-open";"success";"";"rpm";0) | .run_id = "another"),
  warn("o3";"rate limiter store unavailable, failing open"), warn("o3";"token limiter store unavailable, failing open"),
  warn("o4";"rate limiter store unavailable, failing open"), warn("o4";"token limiter store unavailable, failing open"),
  warn("b2";"rate limiter store unavailable, failing open"), warn("b2";"spend store unavailable, failing open"),
  warn("b2";"token limiter store unavailable, failing open"), warn("b2";"spend counter update failed; spend under-counted"),
  warn("c2";"rate limiter store unavailable, failing closed"), warn("o5";"rate limiter store unavailable, failing open"),
  warn("other";"rate limiter store unavailable, failing open")' >"$tmp/base-redis-events.jsonl"

reset_redis() {
  cp "$tmp/base-redis.json" "$tmp/loadtest/out/fixture-hung-redis.json"
  cp "$tmp/base-redis-points.json" "$tmp/loadtest/out/fixture-hung-redis-points.json"
  cp "$tmp/base-redis-window.json" "$tmp/loadtest/out/fixture-hung-redis-window.json"
  cp "$tmp/base-redis-events.jsonl" "$tmp/loadtest/out/fixture-hung-redis-events.jsonl"
}
redis_verify() { bash "$tmp/loadtest/verify-redis.sh" fixture-hung; }
redis_summary() { jq "$1" "$tmp/base-redis.json" >"$tmp/loadtest/out/fixture-hung-redis.json"; }
redis_window() { jq "$1" "$tmp/base-redis-window.json" >"$tmp/loadtest/out/fixture-hung-redis-window.json"; }
redis_points() { jq -c "$1" "$tmp/base-redis-points.json" >"$tmp/loadtest/out/fixture-hung-redis-points.json"; }
redis_events() { jq -c "$1" "$tmp/base-redis-events.jsonl" >"$tmp/loadtest/out/fixture-hung-redis-events.jsonl"; }

reset_redis
expect 0 'consistent store outage with another run ignored' redis_verify
logged 'during  open         2      2      0          0         0         0            505/509.5/509.9 2/2/0'
logged 'during  closed       1      0      0          1         0         0                250/250/250 0/0/0'
logged 'after   open         3      2      0          0         0         1           151/285.1/297.02 1/0/0'
logged 'admitted 2 during the window vs r·W = 10.2 → excess -8.2 (2 fail-open rpm admissions); whole run 5 admitted vs B + r·T = 32'
logged 'lost increments (warnings) 1, 0.034 USD; store counter 0.068 USD = Σcost − Σlost + 0 applied'
logged 'during the window 4 failures answered in 250 ms or more (the last sent at +3 s) and 0 faster; 1 failures sent after the restore, the last at +0.3 s; first store decision after the restore at +0.8 s'
reset_redis
redis_window '.spend_counter_usd = 0.102'
expect 0 'late-applied increment within bounds' redis_verify
logged 'Σcost − Σlost + 0.034 applied after the gateway reported them lost'
reset_redis
redis_window '.spend_counter_usd = 0.05'
expect 1 'spend increment lost without a warning' redis_verify
reset_redis
redis_window '.spend_counter_usd = 0.2'
expect 1 'spend counter above the summed cost' redis_verify
reset_redis
redis_events 'select(.request_id != "b2" or .msg != "spend counter update failed; spend under-counted")'
expect 1 'lost increment without its warning' redis_verify
for id in o1 o2 b1 c2 o5; do
  reset_redis
  redis_events "select(.msg != \"cost_event\" or .request_id != \"$id\")"
  expect 1 "missing store-outage event $id" redis_verify
done
reset_redis
jq -c 'select(.msg == "cost_event" and .request_id == "b2")' "$tmp/base-redis-events.jsonl" >>"$tmp/loadtest/out/fixture-hung-redis-events.jsonl"
expect 1 'duplicate store-outage event' redis_verify
reset_redis
redis_events 'if .request_id == "o1" and .msg == "cost_event" then .tenant = "tenant-budget" else . end'
expect 1 'event of another tenant' redis_verify
reset_redis
redis_events 'if .request_id == "c2" and .msg == "cost_event" then .error_code = "internal" else . end'
expect 1 'fail-closed refusal reclassified' redis_verify
reset_redis
redis_events 'if .request_id == "o2" and .msg == "cost_event" then .outcome = "success" | .error_code = "" else . end'
expect 1 'limited request recorded as admitted' redis_verify
reset_redis
echo '{"msg":"cost_event","run_id":"fixture-hung","request_id":"ghost","tenant":"tenant-open","outcome":"success","error_code":""}' \
  >>"$tmp/loadtest/out/fixture-hung-redis-events.jsonl"
expect 1 'event without a k6 request' redis_verify
reset_redis
redis_summary '.summary.client.closed.store_unavailable = 0'
expect 1 'fail-closed refusals disagree with points' redis_verify
reset_redis
redis_summary '.summary.client.open.transport = 0'
expect 1 'transport failures disagree with points' redis_verify
reset_redis
redis_events 'select(.request_id != "o3" or .msg != "token limiter store unavailable, failing open")'
expect 1 'fail-open decision without its warning' redis_verify
reset_redis
echo '{"msg":"spend store unavailable, failing open","request_id":"o6"}' >>"$tmp/loadtest/out/fixture-hung-redis-events.jsonl"
expect 1 'fail-open warning without the event field' redis_verify
reset_redis
redis_events 'select(.msg != "rate limiter store unavailable, failing closed")'
expect 1 'fail-closed refusal without its warning' redis_verify
reset_redis
redis_events 'if .request_id == "c3" and .msg == "cost_event" then .fail_open = "rpm" else . end'
echo '{"msg":"rate limiter store unavailable, failing open","request_id":"c3"}' >>"$tmp/loadtest/out/fixture-hung-redis-events.jsonl"
expect 1 'fail-closed tenant failed open' redis_verify
logged 'fail-closed tenant events carry fail_open'
reset_redis
redis_summary '.summary.client.open.ok = 4 | .summary.client.open.store_unavailable = 1'
redis_points 'if .data.tags.rid == "o4" then .data.tags.status = "503" | .data.tags.code = "store_unavailable" else . end'
redis_events 'select(.msg != "token limiter store unavailable, failing open" or .request_id != "o4")
  | if .request_id == "o4" and .msg == "cost_event" then .outcome = "error" | .error_code = "store_unavailable" | del(.fail_open)
    elif .request_id == "o4" then .msg = "rate limiter store unavailable, failing closed" else . end'
expect 1 'fail-open tenant refused' redis_verify
logged 'fail-open tenant requests refused with store_unavailable'
reset_redis
redis_events 'if .request_id == "o1" and .msg == "cost_event" then .fail_open = "rpm" else . end'
echo '{"msg":"rate limiter store unavailable, failing open","request_id":"o1"}' >>"$tmp/loadtest/out/fixture-hung-redis-events.jsonl"
expect 1 'store failure before the injection' redis_verify
logged '1 store failures before the injection'
reset_redis
redis_window '.inject_start_ms += 4000 | .inject_end_ms += 4000'
expect 1 'injection without effect' redis_verify
logged 'the injection had no effect'
reset_redis
redis_window '.restore_end_ms = .inject_start_ms - 1'
expect 1 'window boundaries out of order' redis_verify
reset_redis
redis_window '.arm = "flaky"'
expect 1 'unknown arm' redis_verify
reset_redis
redis_window '.spend_counter_usd = null'
expect 1 'spend counter not read' redis_verify
reset_redis
redis_points 'if .data.tags.rid == "b1" then .data.tags.rid = "none" else . end'
expect 1 'gateway answer without a request id' redis_verify
reset_redis
redis_points 'if .data.tags.rid == "c1" then del(.data.tags.sent_ms) else . end'
expect 1 'point without a send time' redis_verify
reset_redis
redis_summary '.summary.thresholds_passed = false | .summary.dropped_iterations = 3'
expect 1 'store-outage k6 thresholds failed' redis_verify
reset_redis
redis_summary 'del(.summary.client.budget)'
expect 1 'missing tenant counters' redis_verify
reset_redis
rm "$tmp/loadtest/out/fixture-hung-redis-window.json"
expect 1 'missing injection record' redis_verify

cat >"$tmp/loadtest/fetch-events.sh" <<'SH'
#!/usr/bin/env bash
echo "fetch:$RUN_ID" >>"$FIXTURE_ROOT/stages"
exit "${FETCH_STATUS:-0}"
SH
for name in verify verify-breaker verify-tpm verify-chaos verify-redis; do
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

outage() { env REDIS_WARMUP=1 REDIS_FAIL_FOR=2 REDIS_DURATION=5 "$@"; }
injection_record() {
  jq -e --arg arm "$1" --arg cmd "docker compose $2 redis" '.arm == $arm and .spend_counter_usd == 0.5 and .commands.inject == $cmd
    and .inject_start_ms <= .inject_end_ms and .inject_end_ms <= .restore_start_ms and .restore_start_ms <= .restore_end_ms' \
    "$tmp/loadtest/out/fixture-$1-redis-window.json"
}
for arm in hung down; do
  if [[ "$arm" == hung ]]; then inject=pause restore=unpause; else inject=stop restore=start; fi
  for scenario in '0 0 0 0 0 0' '7 0 0 0 0 7' '0 5 0 0 0 5' '0 0 6 0 0 6' '0 0 0 8 0 8' '0 0 0 0 9 9' '7 5 6 8 9 7' '0 5 6 8 9 5'; do
    read -r k6_status inject_status restore_status drain_status verify_status expected <<<"$scenario"
    : >"$tmp/stages"
    expect "$expected" "redis $arm stage status $scenario" outage K6_STATUS="$k6_status" INJECT_STATUS="$inject_status" \
      RESTORE_STATUS="$restore_status" DRAIN_STATUS="$drain_status" VERIFY_STATUS="$verify_status" bash "$tmp/loadtest/redis-outage.sh" fixture "$arm"
    printf 'up:\nflush\ninject:%s\nrestore:%s\ndrain:fixture-%s\nverify:fixture-%s\n' "$inject" "$restore" "$arm" "$arm" >"$tmp/expected-stages"
    grep -v '^run:' "$tmp/stages" | diff -u "$tmp/expected-stages" -
    [[ "$(grep -c "^run:fixture-$arm$" "$tmp/stages")" == 1 ]]
  done
  expect 0 "redis $arm injection record" injection_record "$arm" "$inject"
done
: >"$tmp/stages"
expect 1 'store restored when the run aborts inside the window' outage SLEEP_FAIL_ON=2 bash "$tmp/loadtest/redis-outage.sh" fixture hung
grep -v '^run:' "$tmp/stages" | diff -u <(printf 'up:\nflush\ninject:pause\nrestore:unpause\n') -
: >"$tmp/stages"
expect 3 'store-outage setup failure stops before injecting' outage UP_STATUS=3 bash "$tmp/loadtest/redis-outage.sh" fixture down
diff -u <(echo up:) "$tmp/stages"
expect 2 'unknown store-outage arm' bash "$tmp/loadtest/redis-outage.sh" fixture flaky
expect 2 'window longer than the run' env REDIS_WARMUP=30 REDIS_FAIL_FOR=30 REDIS_DURATION=60 bash "$tmp/loadtest/redis-outage.sh" fixture hung
expect 2 'window in a non-integer unit' env REDIS_FAIL_FOR=30s bash "$tmp/loadtest/redis-outage.sh" fixture hung
: >"$tmp/stages"
expect 2 'chaos-redis propagates failure after verifying both arms' env K6_STATUS=7 make -s -C "$tmp" -f "$here/../Makefile" chaos-redis RUN_ID=fixture
grep -q '^verify:fixture-hung$' "$tmp/stages"
grep -q '^verify:fixture-down$' "$tmp/stages"
grep -q '^up:tenant-budget:budget_usd=1000;tenant-closed:fail_closed=true$' "$tmp/stages"
jq -e '.rpm_limit == 600 and .rpm_burst == 20 and .duration_s == 90' "$tmp/loadtest/out/fixture-down-redis-window.json" >/dev/null

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
