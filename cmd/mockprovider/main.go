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
	addr, opts, err := parseFlags(os.Args[1:])
	if err != nil {
		return err
	}
	srv, err := mockprovider.NewServer(logger, opts)
	if err != nil {
		return err
	}

	httpServer := &http.Server{Addr: addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("mock provider listening",
			slog.String("addr", addr),
			slog.Duration("latency_p50", opts.LatencyP50),
			slog.Duration("latency_max", opts.LatencyMax),
			slog.Float64("error_rate", opts.ErrorRate),
			slog.Duration("stream_chunk_delay", opts.StreamChunkDelay),
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

func parseFlags(args []string) (string, mockprovider.Options, error) {
	fs := flag.NewFlagSet("mockprovider", flag.ContinueOnError)
	var opts mockprovider.Options
	addr := fs.String("addr", ":9090", "listen address")
	fs.DurationVar(&opts.LatencyP50, "latency-p50", 0, "typical response latency (e.g. 200ms)")
	fs.DurationVar(&opts.LatencyMax, "latency-max", 0, "largest response latency (e.g. 2s)")
	fs.DurationVar(&opts.LatencyMax, "latency-p99", 0, "same as -latency-max")
	fs.Float64Var(&opts.ErrorRate, "error-rate", 0, "probability [0,1] of responding 500")
	fs.DurationVar(&opts.StreamChunkDelay, "stream-chunk-delay", 10*time.Millisecond, "delay between streamed chunks (e.g. 200ms)")
	if err := fs.Parse(args); err != nil {
		return "", mockprovider.Options{}, err
	}
	return *addr, opts, nil
}
