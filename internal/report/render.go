package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"
)

//go:embed dashboard.html.tmpl
var dashboardHTML string

var dashboard = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"usd":   usd,
	"ms":    func(v int64) string { return fmt.Sprintf("%d ms", v) },
	"tau":   func(v float64) string { return fmt.Sprintf("%.2f", v) },
	"width": width,
	"when":  when,
	"taus":  tauList,
	"names": func(v []string) string { return strings.Join(v, ", ") },
}).Parse(dashboardHTML))

type page struct {
	*Report
	Input     string
	MaxUSD    float64
	MaxP99    int64
	MaxCount  int
	Histogram histogram
}

type histogram struct {
	Width, Height, Base float64
	Bars                []histBar
	TauX                []float64
	Ticks               []histTick
}

type histBar struct {
	X, W                  float64
	MatchY, MatchH        float64
	MismatchY             float64
	MismatchH             float64
	Lo, Hi                float64
	Match, Mismatch       int
	HasMatch, HasMismatch bool
}

type histTick struct {
	X      float64
	Label  string
	Anchor string
}

// WriteHTML renders the self-contained HTML report.
func WriteHTML(w io.Writer, r *Report, input string) error {
	p := page{Report: r, Input: input}
	for _, t := range r.Cost.Tenants {
		p.MaxUSD = max(p.MaxUSD, t.TotalUSD())
	}
	for _, rt := range r.Latency.Routes {
		p.MaxP99 = max(p.MaxP99, rt.P99)
	}
	for _, o := range r.Outcomes.Rows {
		p.MaxCount = max(p.MaxCount, o.Count)
	}
	p.Histogram = buildHistogram(r.Shadow)
	return dashboard.Execute(w, p)
}

func usd(v float64) string { return fmt.Sprintf("$%.6f", v) }

func width(v, limit any) template.CSS {
	f, m := toFloat(v), toFloat(limit)
	if m <= 0 || f <= 0 {
		return "width:0"
	}
	return template.CSS(fmt.Sprintf("width:%.2f%%", 100*f/m))
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}

func when(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02 15:04:05Z")
}

func tauList(taus []float64) string {
	if len(taus) == 0 {
		return "not in this input"
	}
	parts := make([]string, len(taus))
	for i, t := range taus {
		parts[i] = fmt.Sprintf("%.2f", t)
	}
	return strings.Join(parts, ", ")
}

func buildHistogram(v ShadowView) histogram {
	h := histogram{Width: 640, Height: 200, Base: 176}
	peak := 0
	for _, b := range v.Histogram {
		peak = max(peak, b.Match+b.Mismatch)
	}
	if peak == 0 {
		return h
	}
	plot := h.Base - 8
	step := h.Width / float64(len(v.Histogram))
	for i, b := range v.Histogram {
		bar := histBar{
			X:           float64(i)*step + 1,
			W:           step - 2,
			Lo:          b.Lo,
			Hi:          b.Hi,
			Match:       b.Match,
			Mismatch:    b.Mismatch,
			HasMatch:    b.Match > 0,
			HasMismatch: b.Mismatch > 0,
			MatchH:      plot * float64(b.Match) / float64(peak),
			MismatchH:   plot * float64(b.Mismatch) / float64(peak),
		}
		bar.MatchY = h.Base - bar.MatchH
		bar.MismatchY = bar.MatchY - bar.MismatchH
		if bar.HasMatch && bar.HasMismatch {
			bar.MismatchY -= 2
		}
		h.Bars = append(h.Bars, bar)
	}
	for _, t := range v.Taus {
		if t >= 0 && t <= 1 {
			h.TauX = append(h.TauX, t*h.Width)
		}
	}
	for _, t := range []float64{0, 0.25, 0.5, 0.75, 1} {
		anchor := "middle"
		switch t {
		case 0:
			anchor = "start"
		case 1:
			anchor = "end"
		}
		h.Ticks = append(h.Ticks, histTick{X: t * h.Width, Label: fmt.Sprintf("%.2f", t), Anchor: anchor})
	}
	return h
}

// WriteSummary writes the short text summary.
func WriteSummary(w io.Writer, r *Report, input string) error {
	var b strings.Builder
	run := "all runs"
	if r.RunID != "" {
		run = "run_id " + r.RunID
	}
	fmt.Fprintf(&b, "events: %d of %d cost events (%s) from %s\n", r.Selected, r.Read, run, input)
	fmt.Fprintf(&b, "window: %s .. %s\n", when(r.From), when(r.To))
	c := r.Cost.Total
	fmt.Fprintf(&b, "cost: measured %s (%d events), estimated %s (%d events), unpriced %d events\n",
		usd(c.MeasuredUSD), c.Measured, usd(c.EstimatedUSD), c.Estimated, c.Unpriced())
	for _, t := range r.Cost.Tenants {
		fmt.Fprintf(&b, "  tenant %s: %d requests, %d prompt + %d completion tokens, measured %s, estimated %s\n",
			t.Tenant, t.Requests, t.PromptTokens, t.CompletionTokens, usd(t.MeasuredUSD), usd(t.EstimatedUSD))
	}
	if a := r.Latency.All; a.Count > 0 {
		fmt.Fprintf(&b, "latency (successful, nearest-rank): n=%d p50 %d ms, p95 %d ms, p99 %d ms, max %d ms\n",
			a.Count, a.P50, a.P95, a.P99, a.Max)
	} else {
		fmt.Fprintf(&b, "latency: no successful requests\n")
	}
	outcomes := make([]string, 0, len(r.Outcomes.Rows))
	for _, o := range r.Outcomes.Rows {
		name := o.Outcome
		if o.Code != "" {
			name += "/" + o.Code
		}
		outcomes = append(outcomes, fmt.Sprintf("%s %d", name, o.Count))
	}
	fmt.Fprintf(&b, "outcomes: %s\n", strings.Join(outcomes, ", "))
	f := r.Fallback
	fmt.Fprintf(&b, "fallback: %s served after fallback, %d upstream errors unserved, %d breaker transitions\n",
		Ratio{f.AfterFallback, f.Served}, f.Unserved, len(f.Transitions))
	l := r.Limits.Total
	fmt.Fprintf(&b, "limits: 429 request %d, 429 token %d, 402 budget %d, 503 fail-closed %d, fail-open request %d / token %d / spend %d\n",
		l.RequestLimited, l.TokenLimited, l.BudgetExceeded, l.FailClosed, l.FailOpenRequest, l.FailOpenToken, l.FailOpenSpend)
	if s := r.Shadow; s.Enabled {
		fmt.Fprintf(&b, "shadow cache: predicted hit-rate %s, false-positive bound %s (fingerprint mismatches), τ %s\n",
			s.HitRate, s.FalsePos, tauList(s.Taus))
	} else {
		fmt.Fprintf(&b, "shadow cache: no records in this input\n")
	}
	k := r.Skipped
	fmt.Fprintf(&b, "skipped: %d non-JSON lines, %d malformed, %d other records, %d duplicate events, %d records without a cost event\n",
		k.NonJSON, k.Malformed, k.Other, k.Duplicates, k.Orphans)
	_, err := io.WriteString(w, b.String())
	return err
}
