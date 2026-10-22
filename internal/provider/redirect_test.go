package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/core"
)

func TestRedirectsNeverResendProviderPayloads(t *testing.T) {
	for _, status := range []int{307, 308} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%v", status, streaming), func(t *testing.T) {
				var received atomic.Int32
				destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received.Add(1)
					if streaming {
						_, _ = io.WriteString(w, "data: [DONE]\n\n")
					} else {
						_, _ = io.WriteString(w, upstreamBody)
					}
				}))
				defer destination.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL+"/private", status) }))
				defer source.Close()
				p := NewHTTP("mock", source.URL, time.Second, discardLogger(), WithAPIKey("private-redirect-key"))
				var err error
				if streaming {
					var body io.ReadCloser
					body, err = p.StreamComplete(context.Background(), chatReq())
					if body != nil {
						_ = body.Close()
					}
				} else {
					_, err = p.Complete(context.Background(), chatReq())
				}
				if received.Load() != 0 || err == nil || core.AsError(err).Code != core.CodeUpstream {
					t.Fatalf("destination calls=%d error=%v", received.Load(), err)
				}
			})
		}
	}
}

func TestVersionedProviderEndpointUsesHTTPS(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("unexpected destination or credentials")
		}
		received.Add(1)
		if r.Header.Get("Accept") == "text/event-stream" {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		} else {
			_, _ = io.WriteString(w, upstreamBody)
		}
	}))
	defer srv.Close()
	p := NewHTTP("mock", srv.URL+"/v1", time.Second, discardLogger(), WithAPIKey("test-key"))
	p.client.Transport = srv.Client().Transport
	p.streamClient.Transport = srv.Client().Transport
	if _, err := p.Complete(context.Background(), chatReq()); err != nil {
		t.Fatal(err)
	}
	stream, err := p.StreamComplete(context.Background(), chatReq())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(io.Discard, stream); err != nil {
		t.Fatal(err)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 2 {
		t.Fatal("provider requests missing")
	}
}
