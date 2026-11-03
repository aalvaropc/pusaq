package embed

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPReusesConnectionsUnderConcurrency(t *testing.T) {
	loads := []struct {
		name            string
		workers, rounds int
		bursts          bool
	}{
		{"steady", 20, 20, false},
		{"bursts", 150, 10, true},
	}
	for _, load := range loads {
		t.Run(load.name, func(t *testing.T) {
			var opened atomic.Int64
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(5 * time.Millisecond)
				_, _ = io.WriteString(w, `{"data":[{"embedding":[1,0]}]}`)
			}))
			upstream.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					opened.Add(1)
				}
			}
			upstream.Start()
			defer upstream.Close()
			h := NewHTTP("test", upstream.URL, "test-embedding", "", 5*time.Second)
			embed := func() {
				if _, err := h.Embed(context.Background(), "hello"); err != nil {
					t.Error(err)
				}
			}

			if load.bursts {
				for range load.rounds {
					var wg sync.WaitGroup
					for range load.workers {
						wg.Go(embed)
					}
					wg.Wait()
				}
			} else {
				var wg sync.WaitGroup
				for range load.workers {
					wg.Go(func() {
						for range load.rounds {
							embed()
						}
					})
				}
				wg.Wait()
			}

			n := opened.Load()
			t.Logf("%d requests at concurrency %d opened %d connections", load.workers*load.rounds, load.workers, n)
			if n > int64(2*load.workers) {
				t.Fatalf("opened %d connections for concurrency %d, want at most %d", n, load.workers, 2*load.workers)
			}
		})
	}
}
