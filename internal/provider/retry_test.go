package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

type call func(*HTTP) error

func buffered(p *HTTP) error {
	_, err := p.Complete(context.Background(), chatReq())
	return err
}

func streamed(p *HTTP) error {
	s, err := p.StreamComplete(context.Background(), chatReq())
	if s != nil {
		_ = s.Close()
	}
	return err
}

var paths = map[string]call{"buffered": buffered, "stream": streamed}

func TestRetryableStatusesCarryStatusAndRetryAfter(t *testing.T) {
	for name, do := range paths {
		for _, status := range []int{408, 429, 500, 502, 503, 504} {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(status)
			}))
			err := do(NewHTTP("mock", upstream.URL, time.Second, discardLogger()))
			upstream.Close()

			re, ok := errors.AsType[*RetryableError](err)
			if !ok || core.AsError(err).Code != core.CodeUpstream || re.Status != status || re.RetryAfter != 2*time.Second {
				t.Fatalf("%s %d: err = %v, retryable = %+v", name, status, err, re)
			}
		}
	}
}

func TestOtherFailuresAreNotRetryable(t *testing.T) {
	for name, do := range paths {
		for _, status := range []int{400, 401, 403, 404, 413, 422, 501, 505, http.StatusTemporaryRedirect} {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "2")
				w.Header().Set("Location", "/elsewhere")
				w.WriteHeader(status)
			}))
			err := do(NewHTTP("mock", upstream.URL, time.Second, discardLogger()))
			upstream.Close()

			if err == nil {
				t.Fatalf("%s %d: no error", name, status)
			}
			if re, ok := errors.AsType[*RetryableError](err); ok {
				t.Fatalf("%s %d: marked retryable: %+v", name, status, re)
			}
		}
	}
}

func TestTransportFailuresBeforeAResponseAreRetryable(t *testing.T) {
	refused := httptest.NewServer(http.NotFoundHandler())
	refusedURL := refused.URL
	refused.Close()
	silent := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer silent.Close()
	reset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer reset.Close()

	for name, do := range paths {
		for upstream, url := range map[string]string{"refused": refusedURL, "header timeout": silent.URL, "reset": reset.URL} {
			err := do(NewHTTP("mock", url, 50*time.Millisecond, discardLogger()))
			re, ok := errors.AsType[*RetryableError](err)
			if !ok || re.Status != 0 || re.Err == nil || core.AsError(err).Code != core.CodeUpstream {
				t.Fatalf("%s %s: err = %v, retryable = %+v", name, upstream, err, re)
			}
		}
	}
}

func TestBufferedBodyFailureAfterHeadersIsNotRetryable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4096\r\n\r\n{\"id\":")
		_ = rw.Flush()
	}))
	defer upstream.Close()

	err := buffered(NewHTTP("mock", upstream.URL, time.Second, discardLogger()))
	if core.AsError(err).Code != core.CodeUpstream {
		t.Fatalf("err = %v, want upstream_error", err)
	}
	if _, ok := errors.AsType[*RetryableError](err); ok {
		t.Fatalf("a body that failed after the headers is marked retryable: %v", err)
	}
}

func TestTransportFailureKeepsItsCause(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer upstream.Close()

	err := buffered(NewHTTP("mock", upstream.URL, 50*time.Millisecond, discardLogger()))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the timeout reachable through errors.Is", err)
	}
}

func TestRetryAfterForms(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"-3", 0},
		{"3", 3 * time.Second},
		{"1.5", 0},
		{"soon", 0},
		{"99999999999999", maxRetryAfter},
		{now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{now.Add(72 * time.Hour).Format(http.TimeFormat), maxRetryAfter},
	} {
		if got := retryAfter(tc.value, now); got != tc.want {
			t.Fatalf("retryAfter(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestIdempotencyKeyIsAHeaderOnBothPaths(t *testing.T) {
	type seen struct {
		key  string
		has  bool
		body string
	}
	for name, do := range map[string]func(*HTTP, ChatRequest) error{
		"buffered": func(p *HTTP, r ChatRequest) error { _, err := p.Complete(context.Background(), r); return err },
		"stream": func(p *HTTP, r ChatRequest) error {
			s, err := p.StreamComplete(context.Background(), r)
			if err == nil {
				_, _ = io.Copy(io.Discard, s)
				_ = s.Close()
			}
			return err
		},
	} {
		for _, key := range []string{"pusaq-0123456789abcdef", ""} {
			var got seen
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_, got.has = r.Header[IdempotencyKeyHeader]
				got.key, got.body = r.Header.Get(IdempotencyKeyHeader), string(body)
				if strings.Contains(got.body, `"stream":true`) {
					_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\ndata: [DONE]\n\n")
					return
				}
				_, _ = io.WriteString(w, upstreamBody)
			}))
			req := chatReq()
			req.IdempotencyKey = key
			err := do(NewHTTP("mock", upstream.URL, time.Second, discardLogger()), req)
			upstream.Close()
			if err != nil {
				t.Fatalf("%s %q: %v", name, key, err)
			}
			if got.key != key || got.has != (key != "") {
				t.Fatalf("%s: header = %q (present %v), want %q", name, got.key, got.has, key)
			}
			if strings.Contains(strings.ToLower(got.body), "idempotency") || (key != "" && strings.Contains(got.body, key)) {
				t.Fatalf("%s: key leaked into the body %s", name, got.body)
			}
		}
	}
}
