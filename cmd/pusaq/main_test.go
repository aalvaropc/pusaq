package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aalvaropc/pusaq/internal/config"
	"github.com/aalvaropc/pusaq/internal/embed"
	"github.com/aalvaropc/pusaq/internal/ratelimit"

	"github.com/redis/go-redis/v9"
)

func blackhole(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
			go func() { _, _ = io.Copy(io.Discard, c) }()
		}
	}()
	return ln.Addr().String()
}

func TestRedisOptionsHonorContextDeadline(t *testing.T) {
	client := redis.NewClient(redisOptions(blackhole(t)))
	t.Cleanup(func() { _ = client.Close() })
	limiter, err := ratelimit.New(client, "rpm", ratelimit.Limits{Burst: 10, Rate: 1, TTL: time.Minute})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = limiter.Allow(ctx, "acme", 1)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Allow() against a store that never answers succeeded")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Allow() returned after %v, want within the 250ms context deadline", elapsed)
	}
}

func TestHTTPServerTimeouts(t *testing.T) {
	s := newHTTPServer(":0", http.NotFoundHandler())
	if s.ReadHeaderTimeout != readHeaderTimeout || s.IdleTimeout != idleTimeout {
		t.Fatalf("server timeouts = header %v idle %v", s.ReadHeaderTimeout, s.IdleTimeout)
	}
	if s.ReadTimeout != 0 || s.WriteTimeout != 0 {
		t.Fatalf("read/write timeouts = %v/%v, want none so SSE responses are not cut", s.ReadTimeout, s.WriteTimeout)
	}
}

func TestEmbedderUsesItsOwnEndpoint(t *testing.T) {
	if _, ok := newEmbedder(config.Config{}).(embed.Local); !ok {
		t.Fatal("embedder without a URL is not the local one")
	}

	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"embedding":[1,0]}]}`)
	}))
	t.Cleanup(upstream.Close)

	e := newEmbedder(config.Config{EmbedURL: upstream.URL + "/v1", EmbedModel: "m", EmbedKey: "sk-e", UpstreamTimeout: time.Second})
	if _, err := e.Embed(context.Background(), "hello"); err != nil {
		t.Fatalf("Embed() error: %v", err)
	}
	if gotPath != "/v1/embeddings" || gotAuth != "Bearer sk-e" {
		t.Fatalf("path = %q, auth = %q; want /v1/embeddings with the embedder key", gotPath, gotAuth)
	}
}
