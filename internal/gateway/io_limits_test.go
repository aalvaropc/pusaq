package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

type readSentinel struct{ reads int }

func (r *readSentinel) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("body read before authentication")
}

func TestUnauthorizedRequestDoesNotReadPayload(t *testing.T) {
	for _, key := range []string{"", "unknown"} {
		s, events := newAuthedServer(t)
		body := &readSentinel{}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 401 || body.reads != 0 || len(*events) != 1 || (*events)[0].ErrorCode != core.CodeUnauthorized || (*events)[0].RunID != "" {
			t.Fatalf("status=%d reads=%d events=%+v", rec.Code, body.reads, *events)
		}
	}
}

func socketRequest(t *testing.T, s *Server, key string) (net.Conn, *bufio.Reader) {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err = conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: local\r\nContent-Length: 100000\r\nAuthorization: Bearer %s\r\n\r\n", key)
	if err != nil {
		t.Fatal(err)
	}
	return conn, bufio.NewReader(conn)
}

func TestSlowBodyAuthenticationAndDeadline(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			events := make(chan core.CostEvent, 1)
			pipeline := core.Chain(Accounting(func(e core.CostEvent) { events <- e }, discardLogger()), RequestID(), Auth(authKeys()))
			s := NewServer(discardLogger(), pipeline, func(context.Context, *core.Request) (*core.Response, error) {
				t.Error("terminal called")
				return &core.Response{Status: 200}, nil
			})
			s.BodyReadTimeout = 30 * time.Millisecond
			key := "invalid"
			want := 401
			if valid {
				key = "sk-support"
				want = 400
			}
			_, reader := socketRequest(t, s, key)
			start := time.Now()
			resp, err := http.ReadResponse(reader, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != want || time.Since(start) > time.Second {
				t.Fatalf("status=%d duration=%v", resp.StatusCode, time.Since(start))
			}
			select {
			case e := <-events:
				if e.RequestID == "" {
					t.Fatal("missing request ID")
				}
			case <-time.After(time.Second):
				t.Fatal("accounting missing")
			}
		})
	}
}

func TestBodyDeadlineDoesNotLimitHealthyStream(t *testing.T) {
	pipeline := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil))
	s := NewServer(discardLogger(), pipeline, func(context.Context, *core.Request) (*core.Response, error) {
		r, w := io.Pipe()
		go func() {
			defer func() { _ = w.Close() }()
			for range 4 {
				<-time.After(15 * time.Millisecond)
				if _, err := io.WriteString(w, "data: chunk\n\n"); err != nil {
					return
				}
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}()
		return &core.Response{Status: 200, Stream: r}, nil
	})
	s.BodyReadTimeout = 10 * time.Millisecond
	s.WriteIdleTimeout = 40 * time.Millisecond
	srv := httptest.NewServer(s)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("stream=%q err=%v", body, err)
	}
}

type endlessStream struct{ closed atomic.Int32 }

func (*endlessStream) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
func (s *endlessStream) Close() error { s.closed.Add(1); return nil }

func TestStalledClientReleasesStreamOnce(t *testing.T) {
	finalized := make(chan struct{}, 2)
	stream := &endlessStream{}
	pipeline := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil))
	s := NewServer(discardLogger(), pipeline, func(_ context.Context, r *core.Request) (*core.Response, error) {
		r.OnFinalize(func() { finalized <- struct{}{} })
		return &core.Response{Status: 200, Stream: stream}, nil
	})
	s.WriteIdleTimeout = 30 * time.Millisecond
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		s.ServeHTTP(w, r)
	}))
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err = tcp.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = io.WriteString(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: local\r\nContent-Length: 2\r\n\r\n{}"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finalized:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled client retained request")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handler retained resources after finalization")
	}
	if stream.closed.Load() != 1 || len(finalized) != 0 {
		t.Fatalf("close count=%d finalizers=%d", stream.closed.Load(), len(finalized))
	}
}

type failingFlushWriter struct{ *httptest.ResponseRecorder }

func (failingFlushWriter) FlushError() error { return io.ErrClosedPipe }

func TestFlushFailureClosesAndFinalizesOnce(t *testing.T) {
	var finalized atomic.Int32
	stream := &endlessStream{}
	pipeline := core.Chain(Accounting(func(core.CostEvent) {}, discardLogger()), RequestID(), Auth(nil))
	s := NewServer(discardLogger(), pipeline, func(_ context.Context, r *core.Request) (*core.Response, error) {
		r.OnFinalize(func() { finalized.Add(1) })
		return &core.Response{Status: 200, Stream: stream}, nil
	})
	s.ServeHTTP(failingFlushWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")))
	if stream.closed.Load() != 1 || finalized.Load() != 1 {
		t.Fatalf("closes=%d finalizers=%d", stream.closed.Load(), finalized.Load())
	}
}
