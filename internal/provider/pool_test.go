package provider

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

func TestClientsReuseConnectionsUnderConcurrency(t *testing.T) {
	const sse = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	clients := []struct {
		name string
		body string
		call func(*HTTP) error
	}{
		{"buffered", upstreamBody, func(p *HTTP) error {
			_, err := p.Complete(context.Background(), chatReq())
			return err
		}},
		{"stream", sse, func(p *HTTP) error {
			body, err := p.StreamComplete(context.Background(), chatReq())
			if err != nil {
				return err
			}
			_, err = io.Copy(io.Discard, body)
			_ = body.Close()
			return err
		}},
	}
	loads := []struct {
		name            string
		workers, rounds int
		bursts          bool
	}{
		{"steady", 20, 20, false},
		{"bursts", 150, 10, true},
	}
	for _, c := range clients {
		for _, load := range loads {
			t.Run(c.name+"/"+load.name, func(t *testing.T) {
				var opened atomic.Int64
				upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					time.Sleep(5 * time.Millisecond)
					_, _ = io.WriteString(w, c.body)
				}))
				upstream.Config.ConnState = func(_ net.Conn, s http.ConnState) {
					if s == http.StateNew {
						opened.Add(1)
					}
				}
				upstream.Start()
				defer upstream.Close()
				p := NewHTTP("u", upstream.URL, 5*time.Second, discardLogger())
				call := func() {
					if err := c.call(p); err != nil {
						t.Error(err)
					}
				}

				if load.bursts {
					for range load.rounds {
						var wg sync.WaitGroup
						for range load.workers {
							wg.Go(call)
						}
						wg.Wait()
					}
				} else {
					var wg sync.WaitGroup
					for range load.workers {
						wg.Go(func() {
							for range load.rounds {
								call()
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
}
