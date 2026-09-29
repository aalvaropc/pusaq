package mockprovider

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"zero options valid", Options{}, false},
		{"sane latencies valid", Options{LatencyP50: 50 * time.Millisecond, LatencyP99: 200 * time.Millisecond}, false},
		{"p99 below p50 rejected", Options{LatencyP50: 200 * time.Millisecond, LatencyP99: 50 * time.Millisecond}, true},
		{"negative latency rejected", Options{LatencyP50: -time.Millisecond}, true},
		{"error rate above 1 rejected", Options{ErrorRate: 1.5}, true},
		{"negative error rate rejected", Options{ErrorRate: -0.1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCompletionReturnsOpenAIShapedBody(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body.Object != "chat.completion" || len(body.Choices) == 0 || body.Usage.TotalTokens == 0 {
		t.Fatalf("body missing OpenAI-shaped fields: %+v", body)
	}
}

func TestErrorRateInjectsFailures(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{ErrorRate: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 with error-rate 1.0", rec.Code)
	}
}

func TestSampleLatencyBounds(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{
		LatencyP50: 50 * time.Millisecond,
		LatencyP99: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drive the sampler across its whole randomness range.
	seq := []float64{0.0, 0.1, 0.45, 0.89, 0.9, 0.95, 0.999}
	i := 0
	s.random = func() float64 {
		v := seq[i%len(seq)]
		i++
		return v
	}
	for range 50 {
		d := s.sampleLatency()
		if d < 0 || d > 200*time.Millisecond {
			t.Fatalf("sampled latency %s outside [0, p99]", d)
		}
	}
}

func TestZeroLatencyConfigSleepsZero(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	s.sleep = func(d time.Duration) { slept = append(slept, d) }

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if len(slept) != 1 || slept[0] != 0 {
		t.Fatalf("slept = %v, want exactly one zero-duration sleep", slept)
	}
}

func TestStreamingCompletionFraming(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.sleep = func(time.Duration) {}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"mock-model","messages":[{"role":"user","content":"x"}],"stream":true}`))
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream not terminated by [DONE]: %q", body[len(body)-40:])
	}

	var chunks, usageChunks int
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Object string `json:"object"`
			Usage  *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk is not JSON: %q: %v", payload, err)
		}
		if chunk.Object != "chat.completion.chunk" {
			t.Fatalf("chunk object = %q", chunk.Object)
		}
		chunks++
		if chunk.Usage != nil {
			usageChunks++
			if chunk.Usage.TotalTokens == 0 {
				t.Fatal("usage chunk missing token counts")
			}
		}
	}
	if chunks < 3 {
		t.Fatalf("only %d chunks streamed", chunks)
	}
	if usageChunks != 1 {
		t.Fatalf("%d usage chunks, want exactly 1", usageChunks)
	}
}

func readStats(t *testing.T, s *Server) Stats {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats status = %d, want 200", rec.Code)
	}
	var st Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("stats body is not JSON: %v", err)
	}
	return st
}

func completion(model string, stream bool) *http.Request {
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"x"}]`
	if stream {
		body += `,"stream":true`
	}
	body += `}`
	return httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
}

func TestStatsStartAtZero(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	st := readStats(t, s)
	if st.Requests != 0 || st.Errors != 0 || len(st.ByModel) != 0 {
		t.Fatalf("fresh stats = %+v, want all zero", st)
	}
}

func TestStatsCountPerModelIncludingStreams(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.sleep = func(time.Duration) {}
	for _, r := range []*http.Request{
		completion("model-a", false),
		completion("model-a", true),
		completion("model-b", false),
	} {
		s.ServeHTTP(httptest.NewRecorder(), r)
	}
	st := readStats(t, s)
	if st.Requests != 3 || st.Errors != 0 {
		t.Fatalf("requests=%d errors=%d, want 3/0", st.Requests, st.Errors)
	}
	if st.ByModel["model-a"] != 2 || st.ByModel["model-b"] != 1 {
		t.Fatalf("by_model = %v, want model-a:2 model-b:1", st.ByModel)
	}
}

func TestStatsCountInjectedErrorsAsReceived(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{ErrorRate: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		s.ServeHTTP(httptest.NewRecorder(), completion("model-a", false))
	}
	st := readStats(t, s)
	if st.Requests != 4 || st.Errors != 4 || st.ByModel["model-a"] != 4 {
		t.Fatalf("stats = %+v, want 4 received, 4 errors", st)
	}
}

func TestStatsAreRaceFreeUnderConcurrency(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.sleep = func(time.Duration) {}
	const n = 100
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			model := "model-a"
			if i%2 == 1 {
				model = "model-b"
			}
			s.ServeHTTP(httptest.NewRecorder(), completion(model, false))
		}()
	}
	wg.Wait()
	st := s.Stats()
	if st.Requests != n || st.ByModel["model-a"]+st.ByModel["model-b"] != n {
		t.Fatalf("stats = %+v, want %d total", st, n)
	}
}

func TestFailureWindowFailsUntilItEnds(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_700_000_000_000)
	s.now = func() time.Time { return now }

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/fail?for=5s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Failing bool   `json:"failing"`
		From    string `json:"from"`
		Until   string `json:"until"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Failing || body.From == "" || body.Until == "" {
		t.Fatalf("admin body = %s (err %v)", rec.Body.String(), err)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, completion("model-a", false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("inside window status = %d, want 500", rec.Code)
	}
	if !readStats(t, s).Failing {
		t.Fatal("stats do not report failing")
	}

	now = now.Add(6 * time.Second)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, completion("model-a", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("after window status = %d, want 200", rec.Code)
	}
	st := readStats(t, s)
	if st.Failing || st.Errors != 1 || st.Requests != 2 {
		t.Fatalf("stats = %+v, want one error out of two, not failing", st)
	}
}

func TestFailureWindowClear(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/fail?for=1h", nil))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/admin/fail", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, completion("model-a", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status after clear = %d, want 200", rec.Code)
	}
}

func TestFailureWindowRejectsBadDuration(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"", "?for=", "?for=abc", "?for=0s", "?for=-1s"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/fail"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: status = %d, want 400", q, rec.Code)
		}
	}
	if readStats(t, s).Failing {
		t.Fatal("a rejected request opened a window")
	}
}

func TestStreamChunkDelayIsConfigurable(t *testing.T) {
	s, err := NewServer(discardLogger(), Options{StreamChunkDelay: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	s.sleep = func(d time.Duration) { slept = append(slept, d) }

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"mock-model","messages":[{"role":"user","content":"x"}],"stream":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var chunkSleeps int
	for _, d := range slept {
		if d == 0 {
			continue // the zero-latency sample before the first byte
		}
		if d != 250*time.Millisecond {
			t.Fatalf("chunk sleep = %s, want 250ms", d)
		}
		chunkSleeps++
	}
	if chunkSleeps == 0 {
		t.Fatal("no chunk sleeps observed")
	}

	if _, err := NewServer(discardLogger(), Options{StreamChunkDelay: -1}); err == nil {
		t.Fatal("negative chunk delay accepted")
	}
	def, _ := NewServer(discardLogger(), Options{})
	if def.opts.StreamChunkDelay != defaultStreamChunkDelay {
		t.Fatalf("default chunk delay = %s, want %s", def.opts.StreamChunkDelay, defaultStreamChunkDelay)
	}
}
