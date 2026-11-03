package report

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func loadFixture(t *testing.T) *Input {
	t.Helper()
	f, err := os.Open("testdata/events.log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	in, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func parse(t *testing.T, text string) *Input {
	t.Helper()
	in, err := Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestParseFixtureCounts(t *testing.T) {
	in := loadFixture(t)
	got := []int{in.Lines, in.NonJSON, in.Malformed, in.Other, in.Duplicates,
		len(in.Events), len(in.Shadow), len(in.Transitions), len(in.Misses), len(in.FailOpens)}
	want := []int{33, 2, 2, 1, 1, 11, 5, 2, 4, 2}
	if !slices.Equal(got, want) {
		t.Fatalf("lines, nonJSON, malformed, other, duplicates, events, shadow, transitions, misses, failOpens = %v, want %v", got, want)
	}
	if !slices.Equal(in.Taus, []float64{0.95}) || !slices.Equal(in.Embedders, []string{"local"}) {
		t.Fatalf("taus %v embedders %v", in.Taus, in.Embedders)
	}
	if in.TelemetryDropped == nil || *in.TelemetryDropped != 0 || in.ShadowSkipped == nil || *in.ShadowSkipped != 1 {
		t.Fatalf("shutdown counters telemetry=%v shadowSkipped=%v", in.TelemetryDropped, in.ShadowSkipped)
	}
	a1 := in.Events[0]
	if a1.RequestID != "a1" || a1.Source != "pusaq-1" || a1.LatencyMS != 100 || !a1.Priced || a1.CostUSD != 0.001 ||
		a1.ServedProvider != "mock" || a1.Attempts != 1 || a1.PromptTokens != 10 || a1.CompletionTokens != 20 {
		t.Fatalf("first event = %+v", a1)
	}
	if tr := in.Transitions[0]; tr.Backend != "mock" || tr.From != "closed" || tr.To != "open" || tr.Source != "pusaq-1" {
		t.Fatalf("first transition = %+v", tr)
	}
}

func TestParseLineShapes(t *testing.T) {
	long := strings.Repeat("x", 200<<10)
	tests := []struct {
		name string
		line string
		want func(*Input) bool
	}{
		{"plain text", "nginx started", func(in *Input) bool { return in.NonJSON == 1 }},
		{"prefixed text", "mock-1  | listening", func(in *Input) bool { return in.NonJSON == 1 }},
		{"broken JSON", `pusaq-1 | {"msg":"cost_event",`, func(in *Input) bool { return in.Malformed == 1 }},
		{"wrong type", `{"msg":"cost_event","request_id":"r","outcome":"success","latency_ms":"slow"}`, func(in *Input) bool { return in.Malformed == 1 }},
		{"event without latency", `{"msg":"cost_event","request_id":"r","outcome":"success"}`, func(in *Input) bool { return in.Malformed == 1 }},
		{"shadow without similarity", `{"msg":"cache_shadow","request_id":"r","shadow_hit":true}`, func(in *Input) bool { return in.Malformed == 1 }},
		{"fallback without backend", `{"msg":"backend failed, trying next","request_id":"r"}`, func(in *Input) bool { return in.Malformed == 1 }},
		{"other record", `{"msg":"gateway listening","addr":":8080"}`, func(in *Input) bool { return in.Other == 1 }},
		{"no msg", `{"level":"INFO"}`, func(in *Input) bool { return in.Other == 1 }},
		{"long event", `{"msg":"cost_event","request_id":"r","outcome":"success","latency_ms":1,"feature_tag":"` + long + `"}`,
			func(in *Input) bool { return len(in.Events) == 1 }},
		{"crlf and no trailing newline", "{\"msg\":\"cost_event\",\"request_id\":\"r\",\"outcome\":\"error\",\"latency_ms\":0}\r",
			func(in *Input) bool { return len(in.Events) == 1 && in.Events[0].LatencyMS == 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := parse(t, tt.line)
			if in.Lines != 1 || !tt.want(in) {
				t.Fatalf("input = %+v", in)
			}
		})
	}
}

func TestParseCountsRepeatedEventsOnce(t *testing.T) {
	line := `{"msg":"cost_event","request_id":"r","outcome":"success","latency_ms":5,"cost_usd":0.5,"price_table_version":"v"}` + "\n"
	in := parse(t, line+line+"\n\n"+line)
	if len(in.Events) != 1 || in.Duplicates != 2 || in.Lines != 3 {
		t.Fatalf("events %d duplicates %d lines %d", len(in.Events), in.Duplicates, in.Lines)
	}
}

func TestParseSumsShutdownCountersAcrossReplicas(t *testing.T) {
	in := parse(t, `a | {"msg":"gateway stopped","telemetry_dropped":2}
b | {"msg":"gateway stopped","telemetry_dropped":3}
a | {"msg":"shadow cache stopped","shadow_dropped":4,"shadow_skipped":1}`)
	if *in.TelemetryDropped != 5 || *in.ShadowDropped != 4 || *in.ShadowSkipped != 1 {
		t.Fatalf("counters %d %d %d", *in.TelemetryDropped, *in.ShadowDropped, *in.ShadowSkipped)
	}
}
