// Command pusaq runs the gateway. main is a composition root only: load
// config, wire dependencies, serve, and drain gracefully on SIGTERM/SIGINT
// (readiness 503 → in-flight grace → telemetry flush → exit).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aalvaropc/pusaq/internal/config"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/gateway"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/telemetry"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("gateway exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	emitter := telemetry.NewEmitter(telemetry.SlogSink{Logger: logger}, cfg.TelemetryBuffer)

	if len(cfg.APIKeys) == 0 {
		logger.Warn("authentication disabled: no PUSAQ_API_KEYS configured, all requests attributed to the dev tenant")
	}
	pipeline := core.Chain(
		gateway.Accounting(emitter.Emit, logger),
		gateway.RequestID(),
		gateway.Auth(cfg.APIKeys),
	)

	providers := map[string]provider.Provider{
		"mock": provider.NewHTTP("mock", cfg.MockURL, cfg.UpstreamTimeout, logger),
	}
	routes := make(map[string]gateway.Route, len(cfg.Routes))
	for alias, r := range cfg.Routes {
		routes[alias] = gateway.Route{Provider: r.Provider, Model: r.Model}
	}
	completions := gateway.CompletionsHandler(routes, providers)

	srv := gateway.NewServer(logger, pipeline, completions)

	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("gateway listening", slog.String("addr", cfg.Addr))
		serveErr <- httpServer.ListenAndServe()
	}()
	srv.SetReady(true)

	select {
	case err := <-serveErr:
		// Listener failed before any shutdown signal.
		emitter.Close()
		return err
	case <-ctx.Done():
	}

	// Drain: stop advertising readiness and keep serving for the drain
	// delay so load-balancer probes observe the 503, then let in-flight
	// requests finish within the grace period, flush telemetry, and exit.
	logger.Info("draining",
		slog.Duration("drain_delay", cfg.DrainDelay),
		slog.Duration("grace", cfg.ShutdownGrace),
	)
	srv.SetReady(false)
	time.Sleep(cfg.DrainDelay)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		logger.Error("shutdown error", slog.Any("error", err))
	}

	emitter.Close()
	logger.Info("gateway stopped", slog.Uint64("telemetry_dropped", emitter.Dropped()))
	return nil
}
