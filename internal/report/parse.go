// Package report aggregates the gateway's JSON log lines into the local event report.
package report

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	msgCostEvent       = "cost_event"
	msgShadow          = "cache_shadow"
	msgBreakerState    = "breaker_state"
	msgBreakerSkip     = "backend breaker open, skipping"
	msgBackendFailed   = "backend failed, trying next"
	msgRequestFailOpen = "rate limiter store unavailable, failing open"
	msgTokenFailOpen   = "token limiter store unavailable, failing open"
	msgSpendFailOpen   = "spend store unavailable, failing open"
	msgShadowConfig    = "semantic cache in shadow mode: measuring, never serving"
	msgShadowStopped   = "shadow cache stopped"
	msgGatewayStopped  = "gateway stopped"
)

// Store names the limit store a fail-open admission bypassed.
type Store string

// Stores whose unavailability admits requests without a decision.
const (
	StoreRequest Store = "request"
	StoreToken   Store = "token"
	StoreSpend   Store = "spend"
)

// Event is one cost_event record as the telemetry slog sink writes it.
type Event struct {
	Source           string
	RequestID        string
	Timestamp        time.Time
	Tenant           string
	Outcome          string
	ErrorCode        string
	LatencyMS        int64
	RequestedModel   string
	ServedModel      string
	RunID            string
	CacheSkip        string
	ServedProvider   string
	Attempts         int
	PromptTokens     int64
	CompletionTokens int64
	Priced           bool
	CostUSD          float64
	CostEstimated    bool
	PriceVersion     string
}

// ShadowRecord is one cache_shadow evaluation that found a neighbor.
type ShadowRecord struct {
	RequestID           string
	Hit                 bool
	Similarity          float64
	NeighborFingerprint string
	ResponseFingerprint string
	Embedder            string
}

// FingerprintsDiffer reports whether the neighbor's response differs from the request's own.
func (s ShadowRecord) FingerprintsDiffer() bool {
	return s.NeighborFingerprint != s.ResponseFingerprint
}

// Transition is one breaker_state record.
type Transition struct {
	Time    time.Time
	Source  string
	Backend string
	From    string
	To      string
}

// BackendMiss is a backend that did not serve a request: skipped on an open breaker, or failed.
type BackendMiss struct {
	RequestID string
	Backend   string
	Skipped   bool
}

// FailOpen is one admission made without a store decision.
type FailOpen struct {
	RequestID string
	Store     Store
}

// Input is everything read from the log lines, before selection.
type Input struct {
	Lines      int
	NonJSON    int
	Malformed  int
	Other      int
	Duplicates int

	Events      []Event
	Shadow      []ShadowRecord
	Transitions []Transition
	Misses      []BackendMiss
	FailOpens   []FailOpen

	Taus      []float64
	Embedders []string

	TelemetryDropped *uint64
	ShadowDropped    *uint64
	ShadowSkipped    *uint64
}

type line struct {
	Time time.Time `json:"time"`
	Msg  string    `json:"msg"`

	RequestID         string    `json:"request_id"`
	Timestamp         time.Time `json:"timestamp"`
	Tenant            string    `json:"tenant"`
	Outcome           string    `json:"outcome"`
	ErrorCode         string    `json:"error_code"`
	LatencyMS         *int64    `json:"latency_ms"`
	RequestedModel    string    `json:"requested_model"`
	ServedModel       string    `json:"served_model"`
	RunID             string    `json:"run_id"`
	CacheSkip         string    `json:"cache_skip"`
	ServedProvider    string    `json:"served_provider"`
	Attempts          int       `json:"attempts"`
	PromptTokens      int64     `json:"prompt_tokens"`
	CompletionTokens  int64     `json:"completion_tokens"`
	CostUSD           *float64  `json:"cost_usd"`
	PriceTableVersion string    `json:"price_table_version"`
	CostEstimated     bool      `json:"cost_estimated"`

	ShadowHit           *bool    `json:"shadow_hit"`
	BestSimilarity      *float64 `json:"best_similarity"`
	NeighborFingerprint string   `json:"neighbor_fingerprint"`
	ResponseFingerprint string   `json:"response_fingerprint"`
	Embedder            string   `json:"embedder"`

	Backend string `json:"backend"`
	From    string `json:"from"`
	To      string `json:"to"`

	Tau              *float64 `json:"tau"`
	TelemetryDropped *uint64  `json:"telemetry_dropped"`
	ShadowDropped    *uint64  `json:"shadow_dropped"`
	ShadowSkipped    *uint64  `json:"shadow_skipped"`
}

var errIncomplete = errors.New("record lacks the fields its kind requires")

// Parse reads log lines until EOF, counting the lines it cannot use instead of failing on them.
func Parse(r io.Reader) (*Input, error) {
	in := &Input{}
	seen := make(map[string]bool)
	taus := make(map[float64]bool)
	embedders := make(map[string]bool)
	br := bufio.NewReader(r)
	for {
		raw, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(raw)) > 0 {
			in.Lines++
			in.add(raw, seen, taus, embedders)
		}
		if errors.Is(err, io.EOF) {
			return in, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func (in *Input) add(raw []byte, seen map[string]bool, taus map[float64]bool, embedders map[string]bool) {
	start := bytes.IndexByte(raw, '{')
	if start < 0 {
		in.NonJSON++
		return
	}
	source := ""
	if prefix := string(raw[:start]); strings.Contains(prefix, "|") {
		source = strings.TrimSpace(prefix[:strings.LastIndex(prefix, "|")])
	}
	var l line
	if err := json.Unmarshal(bytes.TrimSpace(raw[start:]), &l); err != nil {
		in.Malformed++
		return
	}
	if err := in.addRecord(l, source, seen, taus, embedders); err != nil {
		in.Malformed++
	}
}

func (in *Input) addRecord(l line, source string, seen map[string]bool, taus map[float64]bool, embedders map[string]bool) error {
	switch l.Msg {
	case msgCostEvent:
		if l.RequestID == "" || l.Outcome == "" || l.LatencyMS == nil {
			return errIncomplete
		}
		if seen[l.RequestID] {
			in.Duplicates++
			return nil
		}
		seen[l.RequestID] = true
		in.Events = append(in.Events, eventFrom(l, source))
	case msgShadow:
		if l.RequestID == "" || l.ShadowHit == nil || l.BestSimilarity == nil {
			return errIncomplete
		}
		in.Shadow = append(in.Shadow, ShadowRecord{
			RequestID:           l.RequestID,
			Hit:                 *l.ShadowHit,
			Similarity:          *l.BestSimilarity,
			NeighborFingerprint: l.NeighborFingerprint,
			ResponseFingerprint: l.ResponseFingerprint,
			Embedder:            l.Embedder,
		})
		addName(&in.Embedders, embedders, l.Embedder)
	case msgBreakerState:
		if l.Backend == "" || l.To == "" {
			return errIncomplete
		}
		in.Transitions = append(in.Transitions, Transition{Time: l.Time, Source: source, Backend: l.Backend, From: l.From, To: l.To})
	case msgBreakerSkip, msgBackendFailed:
		if l.RequestID == "" || l.Backend == "" {
			return errIncomplete
		}
		in.Misses = append(in.Misses, BackendMiss{RequestID: l.RequestID, Backend: l.Backend, Skipped: l.Msg == msgBreakerSkip})
	case msgRequestFailOpen, msgTokenFailOpen, msgSpendFailOpen:
		if l.RequestID == "" {
			return errIncomplete
		}
		in.FailOpens = append(in.FailOpens, FailOpen{RequestID: l.RequestID, Store: failOpenStore(l.Msg)})
	case msgShadowConfig:
		if l.Tau != nil && !taus[*l.Tau] {
			taus[*l.Tau] = true
			in.Taus = append(in.Taus, *l.Tau)
		}
		addName(&in.Embedders, embedders, l.Embedder)
	case msgShadowStopped:
		in.ShadowDropped = addCount(in.ShadowDropped, l.ShadowDropped)
		in.ShadowSkipped = addCount(in.ShadowSkipped, l.ShadowSkipped)
	case msgGatewayStopped:
		in.TelemetryDropped = addCount(in.TelemetryDropped, l.TelemetryDropped)
	default:
		in.Other++
	}
	return nil
}

func eventFrom(l line, source string) Event {
	e := Event{
		Source:           source,
		RequestID:        l.RequestID,
		Timestamp:        l.Timestamp,
		Tenant:           l.Tenant,
		Outcome:          l.Outcome,
		ErrorCode:        l.ErrorCode,
		LatencyMS:        *l.LatencyMS,
		RequestedModel:   l.RequestedModel,
		ServedModel:      l.ServedModel,
		RunID:            l.RunID,
		CacheSkip:        l.CacheSkip,
		ServedProvider:   l.ServedProvider,
		Attempts:         l.Attempts,
		PromptTokens:     l.PromptTokens,
		CompletionTokens: l.CompletionTokens,
		CostEstimated:    l.CostEstimated,
		PriceVersion:     l.PriceTableVersion,
	}
	if l.CostUSD != nil {
		e.Priced = true
		e.CostUSD = *l.CostUSD
	}
	return e
}

func failOpenStore(msg string) Store {
	switch msg {
	case msgTokenFailOpen:
		return StoreToken
	case msgSpendFailOpen:
		return StoreSpend
	default:
		return StoreRequest
	}
}

func addName(list *[]string, seen map[string]bool, name string) {
	if name != "" && !seen[name] {
		seen[name] = true
		*list = append(*list, name)
	}
}

func addCount(total, n *uint64) *uint64 {
	if n == nil {
		return total
	}
	sum := *n
	if total != nil {
		sum += *total
	}
	return &sum
}
