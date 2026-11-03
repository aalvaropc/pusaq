package report

import (
	"bytes"
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func render(t *testing.T, r *Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteHTML(&buf, r, "events.log"); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestSummaryGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSummary(&buf, build(t, loadFixture(t), "r1"), "events.log"); err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/summary-r1.golden"
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Fatalf("summary differs from %s:\n%s", golden, buf.String())
	}
}

func TestHTMLIsSelfContained(t *testing.T) {
	html := render(t, build(t, loadFixture(t), "r1"))
	for _, id := range []string{"cost", "latency", "outcomes", "fallback", "limits", "shadow"} {
		if !strings.Contains(html, `<section id="`+id+`">`) {
			t.Errorf("missing view %q", id)
		}
	}
	for _, banned := range []string{"<script", "http://", "https://", " src=", "<link", "@import", "url("} {
		if strings.Contains(strings.ToLower(html), banned) {
			t.Errorf("report contains %q", banned)
		}
	}
	if !strings.Contains(html, `content="default-src 'none'; style-src 'unsafe-inline'; img-src data:"`) {
		t.Error("missing content security policy")
	}
	if !strings.Contains(html, "Figures describe the records in this input only.") {
		t.Error("missing input notice")
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("template rejected a value")
	}
}

func TestHTMLPairsEveryHitRateWithItsBound(t *testing.T) {
	html := render(t, build(t, loadFixture(t), "r1"))
	if !strings.Contains(html, "<div><b>50.0% (2/4)</b><span>predicted hit-rate at the configured τ</span></div>\n<div><b>≤ 50.0% (1/2)</b>") {
		t.Fatal("headline hit-rate is not paired with its false-positive bound")
	}
	rows := regexp.MustCompile(`<tr><td>0\.\d\d( \(configured\))?</td><td>([^<]*)</td><td>([^<]*)</td></tr>`).FindAllStringSubmatch(html, -1)
	if len(rows) != len(SweepTaus) {
		t.Fatalf("sweep rows = %d", len(rows))
	}
	for _, row := range rows {
		if !strings.Contains(row[2], "%") || !strings.HasPrefix(row[3], "≤ ") {
			t.Fatalf("sweep row %q lacks a hit-rate or its bound", row[0])
		}
	}
}

func TestHTMLEscapesLogValues(t *testing.T) {
	in := parse(t, eventLine("x", `<script>alert(1)</script>`, "success", "", 1,
		`,"requested_model":"pusaq/fast","served_model":"\" onmouseover=\"x","served_provider":"<b>","attempts":1`))
	html := render(t, build(t, in, ""))
	if strings.Contains(html, "<script>alert") || strings.Contains(html, `" onmouseover="x`) || strings.Contains(html, "<td><b></td>") {
		t.Fatal("log values reached the page unescaped")
	}
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("escaped tenant missing")
	}
}

func TestHTMLSaysWhenViewsHaveNoData(t *testing.T) {
	html := render(t, build(t, parse(t, eventLine("x", "t", "error", "unauthorized", 1, "")), ""))
	for _, want := range []string{
		"No successful requests in the selection",
		"No selected request reached a backend and no breaker moved.",
		"No limit denials and no fail-open admissions in this selection.",
		"the shadow cache was off for these requests, so there is no hit-rate and no false-positive bound to report",
		"drop counts are written at gateway shutdown and are not in this input",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(html, "predicted hit-rate at the configured") {
		t.Error("hit-rate shown without shadow records")
	}
}
