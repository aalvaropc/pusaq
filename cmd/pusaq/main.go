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
	"strings"
	"syscall"
	"time"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/config"
	"github.com/aalvaropc/pusaq/internal/core"
	"github.com/aalvaropc/pusaq/internal/embed"
	"github.com/aalvaropc/pusaq/internal/gateway"
	"github.com/aalvaropc/pusaq/internal/pricing"
	"github.com/aalvaropc/pusaq/internal/provider"
	"github.com/aalvaropc/pusaq/internal/ratelimit"
	"github.com/aalvaropc/pusaq/internal/semcache"
	"github.com/aalvaropc/pusaq/internal/telemetry"

	"github.com/redis/go-redis/v9"
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

	stages := []core.Stage{
		gateway.Accounting(emitter.Emit, logger),
		gateway.RequestID(),
		gateway.Auth(cfg.APIKeys),
	}
	completionsOpts := gateway.CompletionsOptions{
		ReserveDefault: cfg.TPMReserveDefault,
		Logger:         logger,
	}

	var shadow *semcache.Shadow
	var leased *ratelimit.Leased
	if cfg.CacheMode == config.CacheShadow {
		var embedder embed.Embedder = embed.Local{}
		if name, ok := strings.CutPrefix(cfg.EmbedderKind, "openai:"); ok {
			for _, up := range cfg.Upstreams {
				if up.Name == name {
					embedder = embed.NewHTTP(name, up.URL, cfg.EmbedModel, up.APIKey, cfg.UpstreamTimeout)
				}
			}
		}
		shadow = semcache.NewShadow(embedder, semcache.NewIndex(cfg.CacheMaxEntries),
			logger, cfg.CacheTau, 8, 256)
		completionsOpts.Shadow = shadow
		logger.Info("semantic cache in shadow mode: measuring, never serving",
			slog.String("embedder", embedder.Name()),
			slog.Float64("tau", cfg.CacheTau),
			slog.Int("max_entries", cfg.CacheMaxEntries),
		)
	}
	if cfg.RedisAddr != "" {
		// MaxRetries -1 disables internal retries: the limiter stages own
		// the time budget and fail open past it.
		client := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, MaxRetries: -1})
		rpm, err := ratelimit.New(client, "rpm", ratelimit.Limits{
			Burst: cfg.RPMBurst,
			Rate:  cfg.RPMLimit / 60.0, // requests/minute → tokens/second
			TTL:   config.DefaultBucketTTL,
		})
		if err != nil {
			return err
		}
		var admitter gateway.Admitter = rpm
		if cfg.RateLimitMode == config.RateLimitLease {
			leased = ratelimit.NewLeased(rpm, cfg.LeaseSize, cfg.LeaseTTL)
			admitter = leased
		}
		stages = append(stages, gateway.RateLimit(admitter, logger))
		if cfg.TPMLimit > 0 {
			tpm, err := ratelimit.New(client, "tpm", ratelimit.Limits{
				Burst: cfg.TPMBurst,
				Rate:  cfg.TPMLimit / 60.0, // tokens/minute → tokens/second
				TTL:   config.DefaultBucketTTL,
			})
			if err != nil {
				return err
			}
			completionsOpts.Tokens = tpm
		} else {
			logger.Warn("token limiting disabled: PUSAQ_TPM_LIMIT is 0")
		}
		logger.Info("rate limiting enabled",
			slog.String("redis", cfg.RedisAddr),
			slog.String("mode", cfg.RateLimitMode),
			slog.Int("lease_size", cfg.LeaseSize),
			slog.Duration("lease_ttl", cfg.LeaseTTL),
			slog.Float64("rpm_limit", cfg.RPMLimit),
			slog.Float64("rpm_burst", cfg.RPMBurst),
			slog.Float64("tpm_limit", cfg.TPMLimit),
			slog.Float64("tpm_burst", cfg.TPMBurst),
		)
	} else {
		logger.Warn("rate limiting disabled: no PUSAQ_REDIS_ADDR configured")
	}
	pipeline := core.Chain(stages...)

	// Backends in priority order: the first upstream is the primary for
	// every alias, the rest are its fallback chain. Each gets a breaker.
	providers := make(map[string]provider.Provider, len(cfg.Upstreams))
	breakers := make(map[string]*breaker.Breaker, len(cfg.Upstreams))
	var fallbacks []string
	for i, up := range cfg.Upstreams {
		var opts []provider.Option
		if up.APIKey != "" {
			opts = append(opts, provider.WithAPIKey(up.APIKey))
		}
		providers[up.Name] = provider.NewHTTP(up.Name, up.URL, cfg.UpstreamTimeout, logger, opts...)
		breakers[up.Name] = breaker.New(breaker.Config{})
		if i > 0 {
			fallbacks = append(fallbacks, up.Name)
		}
	}
	primary := cfg.Upstreams[0].Name
	routes := make(map[string]gateway.Route, len(cfg.Routes))
	for alias, r := range cfg.Routes {
		routes[alias] = gateway.Route{Provider: primary, Model: r.Model, Fallbacks: fallbacks}
	}
	completionsOpts.Breakers = func(backend string) *breaker.Breaker { return breakers[backend] }
	if cfg.Prices != nil {
		prices := make(map[string]pricing.Price, len(cfg.Prices))
		for model, p := range cfg.Prices {
			prices[model] = pricing.Price{InputPerM: p.InputPerM, OutputPerM: p.OutputPerM}
		}
		completionsOpts.Prices = pricing.NewTable(cfg.PriceTableVersion, prices)
		logger.Info("price table loaded",
			slog.String("version", cfg.PriceTableVersion),
			slog.Any("models", completionsOpts.Prices.Models()),
		)
	} else {
		logger.Info("no price table configured: cost events carry no cost fields")
	}
	completions := gateway.CompletionsHandler(routes, providers, completionsOpts)

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

	if leased != nil {
		// Return held request tokens to the shared bucket before exit.
		leased.Close()
		st := leased.Stats()
		logger.Info("lease-mode limiter stopped",
			slog.Uint64("acquisitions", st.Acquisitions),
			slog.Uint64("local_admits", st.LocalAdmits),
			slog.Uint64("local_denies", st.LocalDenies),
			slog.Uint64("refunds", st.Refunds),
			slog.Uint64("refund_failures", st.RefundFailures),
			slog.Float64("tokens_lost", st.TokensLost),
		)
	}
	if shadow != nil {
		shadow.Close()
		logger.Info("shadow cache stopped",
			slog.Uint64("shadow_dropped", shadow.Dropped()),
			slog.Uint64("shadow_skipped", shadow.Skipped()),
		)
	}
	emitter.Close()
	logger.Info("gateway stopped", slog.Uint64("telemetry_dropped", emitter.Dropped()))
	return nil
}
