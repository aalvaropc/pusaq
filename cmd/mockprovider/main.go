// Command mockprovider runs Pusaq's fake upstream model provider: a canned
// OpenAI-shaped chat completion with configurable latency and error rate, so
// limiter and failover experiments cost zero tokens.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aalvaropc/pusaq/internal/mockprovider"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("mock provider exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	addr := flag.String("addr", ":9090", "listen address")
	latencyP50 := flag.Duration("latency-p50", 0, "median response latency (e.g. 200ms)")
	latencyP99 := flag.Duration("latency-p99", 0, "tail response latency (e.g. 2s)")
	errorRate := flag.Float64("error-rate", 0, "probability [0,1] of responding 500")
	flag.Parse()

	srv, err := mockprovider.NewServer(logger, mockprovider.Options{
		LatencyP50: *latencyP50,
		LatencyP99: *latencyP99,
		ErrorRate:  *errorRate,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{Addr: *addr, Handler: srv}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("mock provider listening",
			slog.String("addr", *addr),
			slog.Duration("latency_p50", *latencyP50),
			slog.Duration("latency_p99", *latencyP99),
			slog.Float64("error_rate", *errorRate),
		)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		logger.Error("shutdown error", slog.Any("error", err))
	}
	logger.Info("mock provider stopped")
	return nil
}
