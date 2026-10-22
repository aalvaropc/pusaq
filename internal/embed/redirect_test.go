package embed

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRedirectsNeverResendEmbeddingPayloads(t *testing.T) {
	for _, status := range []int{307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var received atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				received.Add(1)
				_, _ = io.WriteString(w, `{"data":[{"embedding":[1,0]}]}`)
			}))
			defer destination.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL+"/private", status) }))
			defer source.Close()
			h := NewHTTP("test", source.URL, "test-embedding", "private-redirect-key", time.Second)
			_, err := h.Embed(context.Background(), "private-prompt")
			if received.Load() != 0 || err == nil {
				t.Fatalf("destination calls=%d error=%v", received.Load(), err)
			}
		})
	}
}

func TestVersionedEmbeddingEndpointUsesHTTPS(t *testing.T) {
	var path, auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"embedding":[1,0]}]}`)
	}))
	defer srv.Close()
	h := NewHTTP("test", srv.URL+"/v1", "test-embedding", "test-key", time.Second)
	h.client.Transport = srv.Client().Transport
	if _, err := h.Embed(context.Background(), "hello"); err != nil || path != "/v1/embeddings" || auth != "Bearer test-key" {
		t.Fatalf("path=%s auth=%s error=%v", path, auth, err)
	}
}
