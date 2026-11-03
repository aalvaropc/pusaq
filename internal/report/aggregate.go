package report

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

// ErrNoEvents means the selection holds no cost events.
var ErrNoEvents = errors.New("no cost_event records in the selection")

// SweepTaus are the thresholds the shadow view recomputes from best_similarity.
var SweepTaus = []float64{0.80, 0.85, 0.90, 0.95, 0.97, 0.99}

// HistogramBins is the number of equal-width similarity bins over [0, 1].
const HistogramBins = 20

// Ratio is a count over a denominator; it has no value when the denominator is zero.
type Ratio struct {
	Num, Den int
}

// Value returns the ratio and whether it is defined.
func (r Ratio) Value() (float64, bool) {
	if r.Den == 0 {
		return 0, false
	}
	return float64(r.Num) / float64(r.Den), true
}

// String renders the ratio as a percentage with its counts.
func (r Ratio) String() string {
	v, ok := r.Value()
	if !ok {
		return fmt.Sprintf("n/a (%d/%d)", r.Num, r.Den)
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", 100*v, r.Num, r.Den)
}

// Report is the aggregated content of every view.
type Report struct {
	RunID    string
	Lines    int
	Read     int
	Selected int
	Skipped  Skipped
	From, To time.Time

	Cost     CostView
	Latency  LatencyView
	Outcomes OutcomeView
	Fallback FallbackView
	Limits   LimitView
	Shadow   ShadowView

	TelemetryDropped *uint64
}

// Skipped counts the input the report did not aggregate.
type Skipped struct {
	NonJSON    int
	Malformed  int
	Other      int
	Duplicates int
	Orphans    int
}

// TenantCost is one tenant's requests, tokens and dollars.
type TenantCost struct {
	Tenant           string
	Requests         int
	Succeeded        int
	PromptTokens     int64
	CompletionTokens int64
	Measured         int
	MeasuredUSD      float64
	Estimated        int
	EstimatedUSD     float64
}

// Unpriced counts the tenant's events without a cost.
func (t TenantCost) Unpriced() int { return t.Requests - t.Measured - t.Estimated }

// TotalUSD adds measured and estimated dollars.
func (t TenantCost) TotalUSD() float64 { return t.MeasuredUSD + t.EstimatedUSD }

// CostView is the per-tenant cost table.
type CostView struct {
	Tenants       []TenantCost
	Total         TenantCost
	PriceVersions []string
}

// RouteLatency holds nearest-rank percentiles of one route's successful requests.
type RouteLatency struct {
	Alias, Model       string
	Count              int
	P50, P95, P99, Max int64
}

// LatencyView is the per-route latency table.
type LatencyView struct {
	Routes   []RouteLatency
	All      RouteLatency
	Excluded int
}

// OutcomeRow counts one outcome and error code.
type OutcomeRow struct {
	Outcome string
	Code    string
	Status  string
	Count   int
	Share   Ratio
}

// OutcomeView is the outcome table.
type OutcomeView struct {
	Rows  []OutcomeRow
	Total int
}

// BackendRow is one backend's part in the selected requests.
type BackendRow struct {
	Backend       string
	FirstChoice   int
	Served        int
	FirstTry      int
	AfterFallback int
	Skipped       int
	Failed        int
}

// AttemptCount counts served requests by backend attempts.
type AttemptCount struct {
	Attempts int
	Count    int
}

// FallbackView is the fallback and breaker table.
type FallbackView struct {
	Backends      []BackendRow
	Served        int
	AfterFallback int
	Unserved      int
	Attempts      []AttemptCount
	Transitions   []Transition
}

// HasData reports whether any request reached a backend or any breaker moved.
func (v FallbackView) HasData() bool {
	return len(v.Backends) > 0 || len(v.Transitions) > 0
}

// TenantLimits is one tenant's limit denials and fail-open admissions.
type TenantLimits struct {
	Tenant          string
	Requests        int
	RequestLimited  int
	TokenLimited    int
	BudgetExceeded  int
	FailClosed      int
	FailOpenRequest int
	FailOpenToken   int
	FailOpenSpend   int
}

// LimitView is the rate-limiting table.
type LimitView struct {
	Tenants []TenantLimits
	Total   TenantLimits
}

// HasData reports whether any limit denied or failed open.
func (v LimitView) HasData() bool {
	t := v.Total
	return t.RequestLimited+t.TokenLimited+t.BudgetExceeded+t.FailClosed+t.FailOpenRequest+t.FailOpenToken+t.FailOpenSpend > 0
}

// SweepRow is the predicted hit-rate and its false-positive bound at one τ.
type SweepRow struct {
	Tau        float64
	Configured bool
	HitRate    Ratio
	FalsePos   Ratio
}

// Bin counts shadow records by similarity and fingerprint agreement.
type Bin struct {
	Lo, Hi   float64
	Match    int
	Mismatch int
}

// ReasonCount counts eligible-cache exclusions by reason.
type ReasonCount struct {
	Reason string
	Count  int
}

// ShadowView is the shadow-cache table.
type ShadowView struct {
	Enabled   bool
	Records   int
	Eligible  int
	Embedders []string
	Taus      []float64
	HitRate   Ratio
	FalsePos  Ratio
	Sweep     []SweepRow
	Histogram []Bin
	Skips     []ReasonCount
	Dropped   *uint64
	Skipped   *uint64
	Responses int
}

// SingleResponse reports whether every record saw the same response, which makes fingerprint agreement uninformative.
func (v ShadowView) SingleResponse() bool { return v.Records > 1 && v.Responses == 1 }

// Options select what Build aggregates.
type Options struct {
	RunID string
}

// Build selects events by run id and aggregates every view.
func Build(in *Input, opts Options) (*Report, error) {
	selected := make(map[string]Event)
	var events []Event
	known := make(map[string]bool, len(in.Events))
	for _, e := range in.Events {
		known[e.RequestID] = true
		if opts.RunID != "" && e.RunID != opts.RunID {
			continue
		}
		selected[e.RequestID] = e
		events = append(events, e)
	}
	if len(events) == 0 {
		if opts.RunID != "" {
			return nil, fmt.Errorf("%w (run_id %q)", ErrNoEvents, opts.RunID)
		}
		return nil, ErrNoEvents
	}

	r := &Report{
		RunID:    opts.RunID,
		Lines:    in.Lines,
		Read:     len(in.Events),
		Selected: len(events),
		Skipped: Skipped{
			NonJSON:    in.NonJSON,
			Malformed:  in.Malformed,
			Other:      in.Other,
			Duplicates: in.Duplicates,
			Orphans:    orphans(in, known),
		},
		TelemetryDropped: in.TelemetryDropped,
	}
	r.From, r.To = span(events)
	r.Cost = costView(events)
	r.Latency = latencyView(events)
	r.Outcomes = outcomeView(events)
	r.Fallback = fallbackView(events, in, selected, opts.RunID != "")
	r.Limits = limitView(events, in.FailOpens, selected)
	r.Shadow = shadowView(events, in, selected)
	return r, nil
}

func orphans(in *Input, known map[string]bool) int {
	n := 0
	for _, s := range in.Shadow {
		if !known[s.RequestID] {
			n++
		}
	}
	for _, m := range in.Misses {
		if !known[m.RequestID] {
			n++
		}
	}
	for _, f := range in.FailOpens {
		if !known[f.RequestID] {
			n++
		}
	}
	return n
}

func span(events []Event) (time.Time, time.Time) {
	from, to := events[0].Timestamp, events[0].Timestamp.Add(time.Duration(events[0].LatencyMS)*time.Millisecond)
	for _, e := range events[1:] {
		if e.Timestamp.Before(from) {
			from = e.Timestamp
		}
		if end := e.Timestamp.Add(time.Duration(e.LatencyMS) * time.Millisecond); end.After(to) {
			to = end
		}
	}
	return from, to
}

func label(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func costView(events []Event) CostView {
	byTenant := make(map[string]*TenantCost)
	versions := make(map[string]bool)
	var v CostView
	for _, e := range events {
		t := byTenant[e.Tenant]
		if t == nil {
			t = &TenantCost{Tenant: label(e.Tenant)}
			byTenant[e.Tenant] = t
		}
		for _, row := range []*TenantCost{t, &v.Total} {
			row.Requests++
			if e.Outcome == string(core.OutcomeSuccess) {
				row.Succeeded++
			}
			row.PromptTokens += e.PromptTokens
			row.CompletionTokens += e.CompletionTokens
			switch {
			case e.Priced && e.CostEstimated:
				row.Estimated++
				row.EstimatedUSD += e.CostUSD
			case e.Priced:
				row.Measured++
				row.MeasuredUSD += e.CostUSD
			}
		}
		if e.Priced && e.PriceVersion != "" && !versions[e.PriceVersion] {
			versions[e.PriceVersion] = true
			v.PriceVersions = append(v.PriceVersions, e.PriceVersion)
		}
	}
	v.Total.Tenant = "all tenants"
	for _, t := range byTenant {
		v.Tenants = append(v.Tenants, *t)
	}
	slices.SortFunc(v.Tenants, func(a, b TenantCost) int {
		if c := cmp.Compare(b.TotalUSD(), a.TotalUSD()); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Requests, a.Requests); c != 0 {
			return c
		}
		return cmp.Compare(a.Tenant, b.Tenant)
	})
	slices.Sort(v.PriceVersions)
	return v
}

// Percentile returns the nearest-rank p-th percentile of an ascending sample.
func Percentile(sorted []int64, p int) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := (p*n + 99) / 100
	rank = max(1, min(rank, n))
	return sorted[rank-1]
}

func routeLatency(alias, model string, sample []int64) RouteLatency {
	slices.Sort(sample)
	return RouteLatency{
		Alias: alias,
		Model: model,
		Count: len(sample),
		P50:   Percentile(sample, 50),
		P95:   Percentile(sample, 95),
		P99:   Percentile(sample, 99),
		Max:   sample[len(sample)-1],
	}
}

func latencyView(events []Event) LatencyView {
	type key struct{ alias, model string }
	samples := make(map[key][]int64)
	var all []int64
	var v LatencyView
	for _, e := range events {
		if e.Outcome != string(core.OutcomeSuccess) {
			v.Excluded++
			continue
		}
		k := key{label(e.RequestedModel), label(e.ServedModel)}
		samples[k] = append(samples[k], e.LatencyMS)
		all = append(all, e.LatencyMS)
	}
	for k, s := range samples {
		v.Routes = append(v.Routes, routeLatency(k.alias, k.model, s))
	}
	slices.SortFunc(v.Routes, func(a, b RouteLatency) int {
		if c := cmp.Compare(a.Alias, b.Alias); c != 0 {
			return c
		}
		return cmp.Compare(a.Model, b.Model)
	})
	if len(all) > 0 {
		v.All = routeLatency("all routes", "", all)
	}
	return v
}

// StatusFor renders the HTTP status a client saw for an outcome and error code.
func StatusFor(outcome, code string) string {
	switch {
	case outcome == string(core.OutcomeSuccess):
		return "200"
	case code == string(core.CodeStreamInterrupted):
		return "200, then cut"
	default:
		return fmt.Sprint((&core.Error{Code: core.Code(code)}).Status())
	}
}

func outcomeView(events []Event) OutcomeView {
	type key struct{ outcome, code string }
	counts := make(map[key]int)
	for _, e := range events {
		counts[key{e.Outcome, e.ErrorCode}]++
	}
	v := OutcomeView{Total: len(events)}
	for k, n := range counts {
		v.Rows = append(v.Rows, OutcomeRow{
			Outcome: k.outcome,
			Code:    k.code,
			Status:  StatusFor(k.outcome, k.code),
			Count:   n,
			Share:   Ratio{n, len(events)},
		})
	}
	slices.SortFunc(v.Rows, func(a, b OutcomeRow) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Outcome, b.Outcome); c != 0 {
			return c
		}
		return cmp.Compare(a.Code, b.Code)
	})
	return v
}

func fallbackView(events []Event, in *Input, selected map[string]Event, windowed bool) FallbackView {
	misses := make(map[string][]BackendMiss)
	for _, m := range in.Misses {
		if _, ok := selected[m.RequestID]; ok {
			misses[m.RequestID] = append(misses[m.RequestID], m)
		}
	}
	rows := make(map[string]*BackendRow)
	row := func(name string) *BackendRow {
		if rows[name] == nil {
			rows[name] = &BackendRow{Backend: name}
		}
		return rows[name]
	}
	attempts := make(map[int]int)
	var v FallbackView
	for _, e := range events {
		ms := misses[e.RequestID]
		for _, m := range ms {
			if m.Skipped {
				row(m.Backend).Skipped++
			} else {
				row(m.Backend).Failed++
			}
		}
		switch {
		case len(ms) > 0:
			row(ms[0].Backend).FirstChoice++
		case e.ServedProvider != "":
			row(e.ServedProvider).FirstChoice++
		}
		if e.ServedProvider == "" {
			if e.ErrorCode == string(core.CodeUpstream) {
				v.Unserved++
			}
			continue
		}
		served := row(e.ServedProvider)
		served.Served++
		v.Served++
		attempts[e.Attempts]++
		if len(ms) > 0 {
			served.AfterFallback++
			v.AfterFallback++
		} else {
			served.FirstTry++
		}
	}
	for _, r := range rows {
		v.Backends = append(v.Backends, *r)
	}
	slices.SortFunc(v.Backends, func(a, b BackendRow) int {
		if c := cmp.Compare(b.FirstChoice, a.FirstChoice); c != 0 {
			return c
		}
		return cmp.Compare(a.Backend, b.Backend)
	})
	for n, c := range attempts {
		v.Attempts = append(v.Attempts, AttemptCount{Attempts: n, Count: c})
	}
	slices.SortFunc(v.Attempts, func(a, b AttemptCount) int { return cmp.Compare(a.Attempts, b.Attempts) })

	from, to := span(events)
	for _, t := range in.Transitions {
		if windowed && (t.Time.Before(from) || t.Time.After(to)) {
			continue
		}
		v.Transitions = append(v.Transitions, t)
	}
	slices.SortStableFunc(v.Transitions, func(a, b Transition) int { return a.Time.Compare(b.Time) })
	return v
}

func limitView(events []Event, failOpens []FailOpen, selected map[string]Event) LimitView {
	byTenant := make(map[string]*TenantLimits)
	tenant := func(name string) *TenantLimits {
		if byTenant[name] == nil {
			byTenant[name] = &TenantLimits{Tenant: label(name)}
		}
		return byTenant[name]
	}
	v := LimitView{Total: TenantLimits{Tenant: "all tenants"}}
	for _, e := range events {
		for _, t := range []*TenantLimits{tenant(e.Tenant), &v.Total} {
			t.Requests++
			switch core.Code(e.ErrorCode) {
			case core.CodeRateLimited:
				if e.RequestedModel == "" {
					t.RequestLimited++
				} else {
					t.TokenLimited++
				}
			case core.CodeBudgetExceeded:
				t.BudgetExceeded++
			case core.CodeStoreUnavailable:
				t.FailClosed++
			}
		}
	}
	for _, f := range failOpens {
		e, ok := selected[f.RequestID]
		if !ok {
			continue
		}
		for _, t := range []*TenantLimits{tenant(e.Tenant), &v.Total} {
			switch f.Store {
			case StoreToken:
				t.FailOpenToken++
			case StoreSpend:
				t.FailOpenSpend++
			default:
				t.FailOpenRequest++
			}
		}
	}
	for _, t := range byTenant {
		v.Tenants = append(v.Tenants, *t)
	}
	slices.SortFunc(v.Tenants, func(a, b TenantLimits) int { return cmp.Compare(a.Tenant, b.Tenant) })
	return v
}

func shadowView(events []Event, in *Input, selected map[string]Event) ShadowView {
	v := ShadowView{
		Embedders: in.Embedders,
		Taus:      in.Taus,
		Dropped:   in.ShadowDropped,
		Skipped:   in.ShadowSkipped,
	}
	skips := make(map[string]int)
	for _, e := range events {
		if e.Outcome != string(core.OutcomeSuccess) {
			continue
		}
		if e.CacheSkip != "" {
			skips[e.CacheSkip]++
			continue
		}
		v.Eligible++
	}
	for reason, n := range skips {
		v.Skips = append(v.Skips, ReasonCount{Reason: reason, Count: n})
	}
	slices.SortFunc(v.Skips, func(a, b ReasonCount) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.Reason, b.Reason)
	})

	var records []ShadowRecord
	for _, s := range in.Shadow {
		if _, ok := selected[s.RequestID]; ok {
			records = append(records, s)
		}
	}
	v.Records = len(records)
	v.Enabled = len(records) > 0 || len(in.Taus) > 0
	if !v.Enabled {
		return v
	}

	var hits, mismatches int
	responses := make(map[string]bool)
	v.Histogram = make([]Bin, HistogramBins)
	for i := range v.Histogram {
		v.Histogram[i].Lo = float64(i) / HistogramBins
		v.Histogram[i].Hi = float64(i+1) / HistogramBins
	}
	for _, s := range records {
		responses[s.ResponseFingerprint] = true
		if s.Hit {
			hits++
			if s.FingerprintsDiffer() {
				mismatches++
			}
		}
		b := &v.Histogram[binOf(s.Similarity)]
		if s.FingerprintsDiffer() {
			b.Mismatch++
		} else {
			b.Match++
		}
	}
	v.Responses = len(responses)
	v.HitRate = Ratio{hits, v.Eligible}
	v.FalsePos = Ratio{mismatches, hits}

	taus := slices.Clone(SweepTaus)
	for _, t := range in.Taus {
		if !slices.Contains(taus, t) {
			taus = append(taus, t)
		}
	}
	slices.Sort(taus)
	for _, tau := range taus {
		var h, m int
		for _, s := range records {
			if s.Similarity >= tau {
				h++
				if s.FingerprintsDiffer() {
					m++
				}
			}
		}
		v.Sweep = append(v.Sweep, SweepRow{
			Tau:        tau,
			Configured: slices.Contains(in.Taus, tau),
			HitRate:    Ratio{h, v.Eligible},
			FalsePos:   Ratio{m, h},
		})
	}
	return v
}

func binOf(similarity float64) int {
	i := int(math.Floor(similarity * HistogramBins))
	return max(0, min(i, HistogramBins-1))
}
