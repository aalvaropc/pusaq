package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("private-upstream-sentinel")
}

func TestDiagnosticsExcludeSensitiveUpstreamData(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, network := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "stream", false: "buffered"}[stream], map[bool]string{true: "network", false: "status"}[network]}, "/"), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(500)
					_, _ = io.WriteString(w, "private-upstream-sentinel")
				}))
				defer upstream.Close()
				var logs bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&logs, nil))
				p := NewHTTP("mock", upstream.URL, time.Second, logger)
				if network {
					p.client.Transport = failingTransport{}
					p.streamClient.Transport = failingTransport{}
				}
				var err error
				if stream {
					_, err = p.StreamComplete(context.Background(), chatReq())
				} else {
					_, err = p.Complete(context.Background(), chatReq())
				}
				if err == nil {
					t.Fatal("expected upstream failure")
				}
				if strings.Contains(logs.String(), "private-upstream-sentinel") || strings.Contains(core.AsError(err).Message, "private-upstream-sentinel") {
					t.Fatal("sensitive upstream data escaped")
				}
				if !strings.Contains(logs.String(), `"provider":"mock"`) {
					t.Fatal("provider diagnostic missing")
				}
			})
		}
	}
}
