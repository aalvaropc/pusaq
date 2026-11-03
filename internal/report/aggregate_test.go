package report

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

func build(t *testing.T, in *Input, runID string) *Report {
	t.Helper()
	r, err := Build(in, Options{RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func eventLine(id, tenant, outcome, code string, latency int64, extra string) string {
	return fmt.Sprintf(`{"msg":"cost_event","request_id":%q,"tenant":%q,"outcome":%q,"error_code":%q,"latency_ms":%d%s}`,
		id, tenant, outcome, code, latency, extra)
}

func TestBuildFixtureRun(t *testing.T) {
	r := build(t, loadFixture(t), "r1")

	if r.Read != 11 || r.Selected != 10 || r.Skipped.Orphans != 1 || r.Skipped.Duplicates != 1 {
		t.Fatalf("read %d selected %d skipped %+v", r.Read, r.Selected, r.Skipped)
	}
	wantFrom := time.Date(2026, 11, 3, 10, 0, 1, 0, time.UTC)
	wantTo := time.Date(2026, 11, 3, 10, 0, 10, int(500*time.Millisecond), time.UTC)
	if !r.From.Equal(wantFrom) || !r.To.Equal(wantTo) {
		t.Fatalf("window %s .. %s", r.From, r.To)
	}

	if len(r.Cost.Tenants) != 2 {
		t.Fatalf("tenants = %+v", r.Cost.Tenants)
	}
	b, a := r.Cost.Tenants[0], r.Cost.Tenants[1]
	if b.Tenant != "tenant-b" || b.Requests != 4 || b.Succeeded != 2 || b.PromptTokens != 10 || b.CompletionTokens != 10 ||
		b.Measured != 2 || !near(b.MeasuredUSD, 0.004) || b.Estimated != 0 || b.Unpriced() != 2 {
		t.Fatalf("tenant-b = %+v", b)
	}
	if a.Tenant != "tenant-a" || a.Requests != 6 || a.Succeeded != 3 || a.PromptTokens != 20 || a.CompletionTokens != 40 ||
		a.Measured != 2 || !near(a.MeasuredUSD, 0.002) || a.Estimated != 1 || !near(a.EstimatedUSD, 0.0005) || a.Unpriced() != 3 {
		t.Fatalf("tenant-a = %+v", a)
	}
	if tot := r.Cost.Total; tot.Requests != 10 || !near(tot.MeasuredUSD, 0.006) || !near(tot.EstimatedUSD, 0.0005) || !near(tot.TotalUSD(), 0.0065) {
		t.Fatalf("total = %+v", tot)
	}
	if !slices.Equal(r.Cost.PriceVersions, []string{"test-v1"}) {
		t.Fatalf("price versions = %v", r.Cost.PriceVersions)
	}

	wantRoutes := []RouteLatency{
		{Alias: "pusaq/best", Model: "model-y", Count: 2, P50: 400, P95: 500, P99: 500, Max: 500},
		{Alias: "pusaq/fast", Model: "model-x", Count: 3, P50: 200, P95: 300, P99: 300, Max: 300},
	}
	if !slices.Equal(r.Latency.Routes, wantRoutes) || r.Latency.Excluded != 5 {
		t.Fatalf("latency = %+v excluded %d", r.Latency.Routes, r.Latency.Excluded)
	}
	if all := r.Latency.All; all.Count != 5 || all.P50 != 300 || all.P95 != 500 || all.P99 != 500 {
		t.Fatalf("all routes = %+v", all)
	}

	wantOutcomes := []OutcomeRow{
		{"success", "", "200", 5, Ratio{5, 10}},
		{"error", "rate_limited", "429", 2, Ratio{2, 10}},
		{"error", "budget_exceeded", "402", 1, Ratio{1, 10}},
		{"error", "store_unavailable", "503", 1, Ratio{1, 10}},
		{"error", "upstream_error", "502", 1, Ratio{1, 10}},
	}
	if !slices.Equal(r.Outcomes.Rows, wantOutcomes) {
		t.Fatalf("outcomes = %+v", r.Outcomes.Rows)
	}

	wantBackends := []BackendRow{
		{Backend: "mock", FirstChoice: 6, Served: 3, FirstTry: 3, Skipped: 1, Failed: 2},
		{Backend: "backup", Served: 2, AfterFallback: 2, Failed: 1},
	}
	f := r.Fallback
	if !slices.Equal(f.Backends, wantBackends) || f.Served != 5 || f.AfterFallback != 2 || f.Unserved != 1 {
		t.Fatalf("fallback = %+v", f)
	}
	if !slices.Equal(f.Attempts, []AttemptCount{{1, 4}, {2, 1}}) || len(f.Transitions) != 1 || f.Transitions[0].To != "open" {
		t.Fatalf("attempts %+v transitions %+v", f.Attempts, f.Transitions)
	}

	wantLimits := []TenantLimits{
		{Tenant: "tenant-a", Requests: 6, RequestLimited: 1, TokenLimited: 1},
		{Tenant: "tenant-b", Requests: 4, BudgetExceeded: 1, FailClosed: 1, FailOpenRequest: 1},
	}
	if !slices.Equal(r.Limits.Tenants, wantLimits) || !r.Limits.HasData() {
		t.Fatalf("limits = %+v", r.Limits.Tenants)
	}

	s := r.Shadow
	if !s.Enabled || s.Records != 3 || s.Eligible != 4 || s.HitRate != (Ratio{2, 4}) || s.FalsePos != (Ratio{1, 2}) {
		t.Fatalf("shadow = %+v", s)
	}
	wantSweep := []SweepRow{
		{0.80, false, Ratio{3, 4}, Ratio{2, 3}},
		{0.85, false, Ratio{3, 4}, Ratio{2, 3}},
		{0.90, false, Ratio{3, 4}, Ratio{2, 3}},
		{0.95, true, Ratio{2, 4}, Ratio{1, 2}},
		{0.97, false, Ratio{1, 4}, Ratio{0, 1}},
		{0.99, false, Ratio{1, 4}, Ratio{0, 1}},
	}
	if !slices.Equal(s.Sweep, wantSweep) {
		t.Fatalf("sweep = %+v", s.Sweep)
	}
	if !slices.Equal(s.Skips, []ReasonCount{{"multi_turn", 1}}) {
		t.Fatalf("skips = %+v", s.Skips)
	}
	if h := s.Histogram; len(h) != HistogramBins || h[18] != (Bin{0.90, 0.95, 0, 1}) || h[19] != (Bin{0.95, 1, 1, 1}) {
		t.Fatalf("histogram tail = %+v %+v", h[18], h[19])
	}
}

func TestBuildWithoutRunKeepsEverything(t *testing.T) {
	r := build(t, loadFixture(t), "")
	if r.Selected != 11 || r.Cost.Tenants[0].Tenant != "tenant-a" || !near(r.Cost.Tenants[0].MeasuredUSD, 0.102) {
		t.Fatalf("selected %d tenants %+v", r.Selected, r.Cost.Tenants)
	}
	if r.Shadow.HitRate != (Ratio{3, 5}) || r.Shadow.FalsePos != (Ratio{2, 3}) {
		t.Fatalf("shadow %v %v", r.Shadow.HitRate, r.Shadow.FalsePos)
	}
	if len(r.Fallback.Transitions) != 2 || r.Limits.Total.FailOpenToken != 1 || r.Latency.All.Max != 9000 {
		t.Fatalf("transitions %d fail-open token %d max %d", len(r.Fallback.Transitions), r.Limits.Total.FailOpenToken, r.Latency.All.Max)
	}
}

func TestBuildEmptySelection(t *testing.T) {
	if _, err := Build(&Input{}, Options{}); !errors.Is(err, ErrNoEvents) {
		t.Fatalf("empty input err = %v", err)
	}
	_, err := Build(loadFixture(t), Options{RunID: "missing"})
	if !errors.Is(err, ErrNoEvents) || !strings.Contains(err.Error(), `"missing"`) {
		t.Fatalf("unknown run err = %v", err)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	hundred := make([]int64, 100)
	for i := range hundred {
		hundred[i] = int64(i + 1)
	}
	twenty := hundred[:20]
	tests := []struct {
		sample []int64
		p      int
		want   int64
	}{
		{hundred, 50, 50}, {hundred, 95, 95}, {hundred, 99, 99}, {hundred, 100, 100},
		{twenty, 50, 10}, {twenty, 95, 19}, {twenty, 99, 20},
		{[]int64{10, 20, 30}, 50, 20}, {[]int64{10, 20, 30}, 95, 30},
		{[]int64{7}, 50, 7}, {[]int64{7}, 99, 7},
		{nil, 50, 0},
	}
	for _, tt := range tests {
		if got := Percentile(tt.sample, tt.p); got != tt.want {
			t.Errorf("Percentile(n=%d, p%d) = %d, want %d", len(tt.sample), tt.p, got, tt.want)
		}
	}
}

func TestLatencyPerRouteUsesOnlyItsSuccesses(t *testing.T) {
	var lines []string
	for i := 100; i >= 1; i-- {
		lines = append(lines, eventLine(fmt.Sprint("a", i), "t", "success", "", int64(i), `,"requested_model":"pusaq/fast","served_model":"m1"`))
	}
	lines = append(lines,
		eventLine("err", "t", "error", "upstream_error", 100000, `,"requested_model":"pusaq/fast","served_model":"m1"`),
		eventLine("other", "t", "success", "", 5000, `,"requested_model":"pusaq/best","served_model":"m2"`))
	r := build(t, parse(t, strings.Join(lines, "\n")), "")
	fast := r.Latency.Routes[1]
	if fast.Alias != "pusaq/fast" || fast.Count != 100 || fast.P50 != 50 || fast.P95 != 95 || fast.P99 != 99 || fast.Max != 100 {
		t.Fatalf("fast route = %+v", fast)
	}
	if r.Latency.Excluded != 1 || r.Latency.All.Count != 101 || r.Latency.All.Max != 5000 {
		t.Fatalf("excluded %d all %+v", r.Latency.Excluded, r.Latency.All)
	}
}

func TestCostKeepsEstimatedApart(t *testing.T) {
	in := parse(t, strings.Join([]string{
		eventLine("1", "acme", "success", "", 1, `,"prompt_tokens":3,"completion_tokens":4,"cost_usd":0.25,"price_table_version":"v1"`),
		eventLine("2", "acme", "success", "", 1, `,"cost_usd":0.5,"price_table_version":"v1","cost_estimated":true`),
		eventLine("3", "beta", "success", "", 1, `,"prompt_tokens":1,"completion_tokens":1,"cost_usd":0.125,"price_table_version":"v2"`),
		eventLine("4", "beta", "error", "unauthorized", 1, ""),
		eventLine("5", "", "error", "unauthorized", 1, ""),
	}, "\n"))
	r := build(t, in, "")
	want := []TenantCost{
		{Tenant: "acme", Requests: 2, Succeeded: 2, PromptTokens: 3, CompletionTokens: 4, Measured: 1, MeasuredUSD: 0.25, Estimated: 1, EstimatedUSD: 0.5},
		{Tenant: "beta", Requests: 2, Succeeded: 1, PromptTokens: 1, CompletionTokens: 1, Measured: 1, MeasuredUSD: 0.125},
		{Tenant: "(none)", Requests: 1},
	}
	if !slices.Equal(r.Cost.Tenants, want) {
		t.Fatalf("tenants = %+v", r.Cost.Tenants)
	}
	if tot := r.Cost.Total; tot.MeasuredUSD != 0.375 || tot.EstimatedUSD != 0.5 || tot.Unpriced() != 2 {
		t.Fatalf("total = %+v", tot)
	}
	if !slices.Equal(r.Cost.PriceVersions, []string{"v1", "v2"}) {
		t.Fatalf("versions = %v", r.Cost.PriceVersions)
	}
}

func TestLimitKindFromRequestedModel(t *testing.T) {
	in := parse(t, strings.Join([]string{
		eventLine("rpm", "t", "error", "rate_limited", 0, ""),
		eventLine("tpm", "t", "error", "rate_limited", 0, `,"requested_model":"pusaq/fast","served_model":"m"`),
		eventLine("ok", "t", "success", "", 0, `,"requested_model":"pusaq/fast","served_model":"m"`),
		`{"msg":"token limiter store unavailable, failing open","request_id":"ok"}`,
		`{"msg":"spend store unavailable, failing open","request_id":"ok"}`,
		`{"msg":"rate limiter store unavailable, failing open","request_id":"elsewhere"}`,
	}, "\n"))
	r := build(t, in, "")
	want := TenantLimits{Tenant: "t", Requests: 3, RequestLimited: 1, TokenLimited: 1, FailOpenToken: 1, FailOpenSpend: 1}
	if !slices.Equal(r.Limits.Tenants, []TenantLimits{want}) || r.Skipped.Orphans != 1 {
		t.Fatalf("limits = %+v orphans %d", r.Limits.Tenants, r.Skipped.Orphans)
	}
}

func TestShadowHitRateAlwaysCarriesItsBound(t *testing.T) {
	shadow := func(id string, sim float64, hit bool, response string) string {
		return fmt.Sprintf(`{"msg":"cache_shadow","request_id":%q,"shadow_hit":%t,"best_similarity":%g,"neighbor_fingerprint":"n","response_fingerprint":%q,"embedder":"local"}`,
			id, hit, sim, response)
	}
	lines := []string{`{"msg":"semantic cache in shadow mode: measuring, never serving","embedder":"local","tau":0.92}`}
	for i := range 10 {
		lines = append(lines, eventLine(fmt.Sprint(i), "t", "success", "", 1, ""))
	}
	lines = append(lines,
		eventLine("skip", "t", "success", "", 1, `,"cache_skip":"temperature"`),
		shadow("0", 1.0, true, "n"),
		shadow("1", 0.93, true, "different"),
		shadow("2", 0.91, false, "different"),
		shadow("3", -0.2, false, "n"))
	r := build(t, parse(t, strings.Join(lines, "\n")), "")
	s := r.Shadow
	if s.Eligible != 10 || s.HitRate != (Ratio{2, 10}) || s.FalsePos != (Ratio{1, 2}) {
		t.Fatalf("hit-rate %v bound %v eligible %d", s.HitRate, s.FalsePos, s.Eligible)
	}
	var taus []float64
	for _, row := range s.Sweep {
		taus = append(taus, row.Tau)
		if row.HitRate.Den != s.Eligible || row.FalsePos.Den != row.HitRate.Num {
			t.Fatalf("sweep row %+v does not pair its hit-rate with its bound", row)
		}
		if row.Configured != (row.Tau == 0.92) {
			t.Fatalf("configured flag on %+v", row)
		}
	}
	if !slices.Equal(taus, []float64{0.80, 0.85, 0.90, 0.92, 0.95, 0.97, 0.99}) {
		t.Fatalf("sweep taus = %v", taus)
	}
	if s.Histogram[0].Match != 1 || s.Histogram[19].Match != 1 || s.Histogram[18].Mismatch != 2 {
		t.Fatalf("histogram = %+v", s.Histogram)
	}

	noHits := build(t, parse(t, eventLine("x", "t", "success", "", 1, "")+"\n"+shadow("x", 0.5, false, "n")), "")
	if _, ok := noHits.Shadow.FalsePos.Value(); ok || noHits.Shadow.FalsePos.String() != "n/a (0/0)" || noHits.Shadow.HitRate.String() != "0.0% (0/1)" {
		t.Fatalf("no-hit shadow = %v / %v", noHits.Shadow.HitRate, noHits.Shadow.FalsePos)
	}
}

func TestShadowOffWithoutRecords(t *testing.T) {
	r := build(t, parse(t, eventLine("x", "t", "success", "", 1, "")), "")
	if r.Shadow.Enabled || r.Shadow.Sweep != nil || r.Fallback.HasData() || r.Limits.HasData() {
		t.Fatalf("report = %+v", r)
	}
}

func TestStatusFor(t *testing.T) {
	tests := map[[2]string]string{
		{"success", ""}:                   "200",
		{"error", "stream_interrupted"}:   "200, then cut",
		{"error", "client_canceled"}:      "499",
		{"panic", "internal"}:             "500",
		{"error", "not_ready"}:            "503",
		{"error", "something_unexpected"}: "500",
	}
	for in, want := range tests {
		if got := StatusFor(in[0], in[1]); got != want {
			t.Errorf("StatusFor(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
