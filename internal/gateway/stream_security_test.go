package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/provider"
)

func TestStreamWithholdsProviderErrors(t *testing.T) {
	valid := "data: {\"choices\":[{\"delta\":{\"content\":\"safe\"}}]}\n\n"
	for _, prefix := range []string{"", valid} {
		for _, event := range []string{
			"data: {\"error\":{\"message\":\"private-stream-sentinel\"}}\n\n",
			"data: {\"error\":\"private-stream-sentinel\"}\r\n\r\n",
			"event: error\ndata: private-stream-sentinel\n\n",
			"data: {\"error\":{\"message\":\"private-stream-sentinel\"}}",
		} {
			for _, oneByte := range []bool{false, true} {
				var r io.Reader = strings.NewReader(prefix + event + "data: [DONE]\n\n")
				if !strings.HasSuffix(event, "\n") {
					r = strings.NewReader(prefix + event)
				}
				if oneByte {
					r = iotest.OneByteReader(r)
				}
				scanner := &usageScanningStream{inner: io.NopCloser(r), req: &core.Request{}}
				out, err := drain(scanner)
				if err == nil || !scanner.failed || scanner.completed() || out != prefix+streamErrorEvent {
					t.Fatalf("event=%q single=%v output=%q err=%v", event, oneByte, out, err)
				}
			}
		}
	}
}

func TestSemanticStreamFailureSettlesWithoutFallbackOrShadow(t *testing.T) {
	primary := &scriptedProvider{name: "primary", sse: "data: {\"error\":{\"message\":\"private-stream-sentinel\"}}\n\ndata: [DONE]\n\n"}
	backup := &scriptedProvider{name: "backup", sse: fakeSSE}
	brs, br := tightBreakers(t)
	shadow := &recordingShadow{}
	var events []core.CostEvent
	pipeline := core.Chain(Accounting(func(e core.CostEvent) { events = append(events, e) }, discardLogger()), RequestID(), Auth(nil))
	h := CompletionsHandler(fallbackRoutes(), map[string]provider.Provider{"primary": primary, "backup": backup}, CompletionsOptions{Breakers: brs, Shadow: shadow, Logger: discardLogger()})
	s := NewServer(discardLogger(), pipeline, h)
	for range 2 {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(streamingPayload()))))
		if strings.Count(rec.Body.String(), `"upstream_error"`) != 1 || strings.Contains(rec.Body.String(), "private-stream-sentinel") {
			t.Fatal("invalid error response")
		}
	}
	if len(events) != 2 || events[0].ErrorCode != core.CodeUpstream || events[1].ErrorCode != core.CodeUpstream || backup.calls != 0 || len(shadow.all()) != 0 || br["primary"].State() != breaker.StateOpen {
		t.Fatalf("events=%+v backup=%d shadow=%d breaker=%v", events, backup.calls, len(shadow.all()), br["primary"].State())
	}
}

type repeatedEvents struct {
	event     string
	remaining int
	current   strings.Reader
	done      bool
}

func (r *repeatedEvents) Read(p []byte) (int, error) {
	for {
		n, err := r.current.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != io.EOF {
			return n, err
		}
		if r.remaining > 0 {
			r.current.Reset(r.event)
			r.remaining--
			continue
		}
		if !r.done {
			r.current.Reset("data: [DONE]\n\n")
			r.done = true
			continue
		}
		return 0, io.EOF
	}
}

func TestStreamRetainedMemoryIndependentOfContentSize(t *testing.T) {
	content := strings.Repeat("x", 4096)
	event := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
	const count = (64 << 20) / 4096
	expected := sha256.New()
	for range count {
		_, _ = io.WriteString(expected, content)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	scanner := &usageScanningStream{inner: io.NopCloser(&repeatedEvents{event: event, remaining: count}), req: &core.Request{}}
	if _, err := io.Copy(io.Discard, scanner); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if retained > 2<<20 {
		t.Fatalf("retained %d bytes for 64 MiB stream", retained)
	}
	if scanner.contentFingerprint() != hex.EncodeToString(expected.Sum(nil)[:8]) {
		t.Fatal("fingerprint changed")
	}
	t.Logf("64 MiB content; retained heap delta %d bytes", retained)
	runtime.KeepAlive(scanner)
}

func TestStreamEventLimitIncludesRawFraming(t *testing.T) {
	for _, extra := range []int{0, 1} {
		event := "data: " + strings.Repeat("x", maxEventBytes-len("data: \n\n")+extra) + "\n\n"
		scanner := &usageScanningStream{inner: io.NopCloser(strings.NewReader(event + "data: [DONE]\n\n")), req: &core.Request{}}
		out, err := drain(scanner)
		if extra == 0 {
			if err != nil || out != event+"data: [DONE]\n\n" {
				t.Fatal("event at cap rejected")
			}
		} else if err != errEventTooLarge || out != streamErrorEvent {
			t.Fatal("oversized event escaped")
		}
	}
}
