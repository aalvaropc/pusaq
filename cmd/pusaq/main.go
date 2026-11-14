// Command pusaq runs the gateway. main is a composition root only: load
// config, wire dependencies, serve, and drain gracefully on SIGTERM/SIGINT
// (readiness 503 → in-flight grace → telemetry flush → exit).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	"github.com/aalvaropc/pusaq/internal/retry"
	"github.com/aalvaropc/pusaq/internal/semcache"
	"github.com/aalvaropc/pusaq/internal/spend"
	"github.com/aalvaropc/pusaq/internal/telemetry"

	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger, os.LookupEnv); err != nil {
		logger.Error("gateway exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger, lookup func(string) (string, bool)) error {
	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}
	a, err := newApp(cfg, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		a.emitter.Close()
		return err
	}
	return a.serve(ctx, stop, ln)
}

type app struct {
	cfg      config.Config
	logger   *slog.Logger
	emitter  *telemetry.Emitter
	srv      *gateway.Server
	leased   *ratelimit.Leased
	index    *semcache.Index
	shadow   *semcache.Shadow
	breakers map[string]*breaker.Breaker
	retries  gateway.Retries
}

func newApp(cfg config.Config, logger *slog.Logger) (*app, error) {
	a := &app{cfg: cfg, logger: logger, emitter: telemetry.NewEmitter(telemetry.SlogSink{Logger: logger}, cfg.TelemetryBuffer)}

	if len(cfg.APIKeys) == 0 {
		logger.Warn("authentication disabled: explicit development mode, all requests attributed to the dev tenant")
	}

	emit := func(e core.CostEvent) {
		e.Region = cfg.Region
		a.emitter.Emit(e)
	}
	stages := []core.Stage{
		gateway.Accounting(emit, logger),
		gateway.RequestID(),
		gateway.Auth(cfg.APIKeys),
	}
	opts := gateway.CompletionsOptions{
		ReserveDefault: cfg.TPMReserveDefault,
		Logger:         logger,
		Policies:       tenantPolicies(cfg, logger),
	}
	if cfg.CacheMode == config.CacheShadow {
		a.startShadow()
		opts.Shadow = a.shadow
	}
	limit, err := a.limits(&opts)
	if err != nil {
		return nil, err
	}
	if limit != nil {
		stages = append(stages, limit)
	}

	providers, fallbacks := a.upstreams()
	if cfg.RetryMax > 0 {
		a.retries = gateway.Retries{Max: cfg.RetryMax, Window: cfg.RetryWindow, Budget: retry.NewBudget(cfg.RetryBudgetRatio)}
	}
	opts.Retries = a.retries
	logger.Info("upstream retries configured",
		slog.Int("max", cfg.RetryMax),
		slog.Float64("budget_ratio", cfg.RetryBudgetRatio),
		slog.Duration("window", cfg.RetryWindow),
	)
	primary := cfg.Upstreams[0].Name
	routes := make(map[string]gateway.Route, len(cfg.Routes))
	for alias, r := range cfg.Routes {
		routes[alias] = gateway.Route{Provider: primary, Model: r.Model, Fallbacks: fallbacks}
	}
	breakers := a.breakers
	opts.Breakers = func(backend string) *breaker.Breaker { return breakers[backend] }
	opts.Rules = gateway.RoutingRules{ByTag: cfg.RoutingRules, Default: cfg.RoutingDefault}
	logger.Info("routing rules loaded",
		slog.Any("rules", cfg.RoutingRules),
		slog.String("default", cfg.RoutingDefault),
	)
	opts.Prices = priceTable(cfg, logger)

	a.srv = gateway.NewServer(logger, core.Chain(stages...), gateway.CompletionsHandler(routes, providers, opts),
		gateway.WithRegion(cfg.Region),
		gateway.WithAdminToken(cfg.AdminToken),
	)
	a.srv.BodyReadTimeout = cfg.BodyReadTimeout
	a.srv.WriteIdleTimeout = cfg.WriteIdleTimeout
	if cfg.AdminToken != "" {
		logger.Warn("admin endpoints enabled: readiness can be failed on purpose with the admin token")
	}
	return a, nil
}

func tenantPolicies(cfg config.Config, logger *slog.Logger) gateway.PolicyFor {
	if len(cfg.TenantPolicies) == 0 {
		return nil
	}
	table := make(map[string]gateway.Policy, len(cfg.TenantPolicies))
	blocking := 0
	for tenant, p := range cfg.TenantPolicies {
		gp := gateway.Policy{
			MaxPromptChars: p.MaxPromptChars,
			BudgetUSD:      p.BudgetUSD,
			FailClosed:     p.FailClosed,
			BlockSensitive: p.Sensitive == "block",
			BlockInjection: p.Injection == "block",
			CacheOff:       p.CacheOff,
		}
		if gp.BlockSensitive || gp.BlockInjection {
			blocking++
		}
		if p.Models != nil {
			gp.Models = make(map[string]bool, len(p.Models))
			for _, alias := range p.Models {
				gp.Models[alias] = true
			}
		}
		table[tenant] = gp
	}
	logger.Info("tenant policies loaded",
		slog.Int("tenants", len(table)),
		slog.Bool("budgets", cfg.HasBudgets()),
		slog.Int("blocking_content_rules", blocking),
	)
	return func(tenant string) gateway.Policy { return table[tenant] }
}

func (a *app) startShadow() {
	cfg := a.cfg
	embedder := newEmbedder(cfg)
	a.index = semcache.NewIndex(cfg.CacheMaxEntries, cfg.CacheMaxBytes)
	a.shadow = semcache.NewShadow(embedder, a.index, a.logger, cfg.CacheTau, 8, 256, cfg.CacheQueueMaxBytes)
	a.logger.Info("semantic cache in shadow mode: measuring, never serving",
		slog.String("embedder", embedder.Name()),
		slog.Float64("tau", cfg.CacheTau),
		slog.Int("max_entries", cfg.CacheMaxEntries),
		slog.Int64("max_bytes", cfg.CacheMaxBytes),
		slog.Int64("pending_max_bytes", cfg.CacheQueueMaxBytes),
	)
}

func (a *app) limits(opts *gateway.CompletionsOptions) (core.Stage, error) {
	cfg, logger := a.cfg, a.logger
	if cfg.RedisAddr == "" {
		logger.Warn("rate limiting disabled: no PUSAQ_REDIS_ADDR configured")
		return nil, nil
	}
	client := redis.NewClient(redisOptions(cfg.RedisAddr))
	rpm, err := ratelimit.New(client, "rpm", ratelimit.Limits{
		Burst: cfg.RPMBurst,
		Rate:  cfg.RPMLimit / 60.0,
		TTL:   bucketTTLFloor,
	})
	if err != nil {
		return nil, err
	}
	var admitter gateway.Admitter = rpm
	if cfg.RateLimitMode == config.RateLimitLease {
		a.leased = ratelimit.NewLeased(rpm, cfg.LeaseSize, cfg.LeaseTTL)
		admitter = a.leased
	}
	stage := gateway.RateLimit(admitter, opts.Policies, logger)
	if cfg.HasBudgets() {
		opts.Spend = spend.New(client)
		logger.Info("tenant budgets enabled: monthly spend counted in the store")
	}
	if cfg.TPMLimit > 0 {
		tpm, err := ratelimit.New(client, "tpm", ratelimit.Limits{
			Burst: cfg.TPMBurst,
			Rate:  cfg.TPMLimit / 60.0,
			TTL:   bucketTTLFloor,
		})
		if err != nil {
			return nil, err
		}
		opts.Tokens = tpm
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
	return stage, nil
}

func (a *app) upstreams() (map[string]provider.Provider, []string) {
	cfg, logger := a.cfg, a.logger
	providers := make(map[string]provider.Provider, len(cfg.Upstreams))
	a.breakers = make(map[string]*breaker.Breaker, len(cfg.Upstreams))
	var fallbacks []string
	for i, up := range cfg.Upstreams {
		var opts []provider.Option
		if up.APIKey != "" {
			opts = append(opts, provider.WithAPIKey(up.APIKey))
		}
		providers[up.Name] = provider.NewHTTP(up.Name, up.URL, cfg.UpstreamTimeout, logger, opts...)
		backend := up.Name
		a.breakers[backend] = breaker.New(breaker.Config{
			Window:           cfg.BreakerWindow,
			MinSamples:       cfg.BreakerMinSamples,
			FailureThreshold: cfg.BreakerThreshold,
			Cooldown:         cfg.BreakerCooldown,
			Probes:           cfg.BreakerProbes,
			OnStateChange: func(from, to breaker.State) {
				logger.Info("breaker_state",
					slog.String("backend", backend),
					slog.String("from", string(from)),
					slog.String("to", string(to)),
				)
			},
		})
		if i > 0 {
			fallbacks = append(fallbacks, up.Name)
		}
	}
	logger.Info("circuit breakers configured",
		slog.Int("window", cfg.BreakerWindow),
		slog.Int("min_samples", cfg.BreakerMinSamples),
		slog.Float64("threshold", cfg.BreakerThreshold),
		slog.Duration("cooldown", cfg.BreakerCooldown),
		slog.Int("probes", cfg.BreakerProbes),
	)
	return providers, fallbacks
}

func priceTable(cfg config.Config, logger *slog.Logger) *pricing.Table {
	if cfg.Prices == nil {
		logger.Info("no price table configured: cost events carry no cost fields")
		return nil
	}
	prices := make(map[string]pricing.Price, len(cfg.Prices))
	for model, p := range cfg.Prices {
		prices[model] = pricing.Price{InputPerM: p.InputPerM, OutputPerM: p.OutputPerM}
	}
	table := pricing.NewTable(cfg.PriceTableVersion, prices)
	logger.Info("price table loaded",
		slog.String("version", cfg.PriceTableVersion),
		slog.Any("models", table.Models()),
	)
	return table
}

func (a *app) serve(ctx context.Context, stop context.CancelFunc, ln net.Listener) error {
	httpServer := newHTTPServer(a.cfg.Addr, a.srv)
	serveErr := make(chan error, 1)
	go func() {
		a.logger.Info("gateway listening", slog.String("addr", a.cfg.Addr), slog.String("region", a.cfg.Region))
		serveErr <- httpServer.Serve(ln)
	}()
	a.srv.SetReady(true)

	select {
	case err := <-serveErr:
		a.emitter.Close()
		return err
	case <-ctx.Done():
	}
	stop()

	// Probes see the 503 during the drain delay; in-flight requests then get the grace period.
	a.logger.Info("draining",
		slog.Duration("drain_delay", a.cfg.DrainDelay),
		slog.Duration("grace", a.cfg.ShutdownGrace),
	)
	a.srv.SetReady(false)
	time.Sleep(a.cfg.DrainDelay)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		a.logger.Error("shutdown error", slog.Any("error", err))
	}
	a.close()
	return nil
}

func (a *app) close() {
	if a.leased != nil {
		a.leased.Close()
		st := a.leased.Stats()
		a.logger.Info("lease-mode limiter stopped",
			slog.Uint64("acquisitions", st.Acquisitions),
			slog.Uint64("local_admits", st.LocalAdmits),
			slog.Uint64("local_denies", st.LocalDenies),
			slog.Uint64("refunds", st.Refunds),
			slog.Uint64("refund_failures", st.RefundFailures),
			slog.Float64("tokens_lost", st.TokensLost),
		)
	}
	if a.shadow != nil {
		a.shadow.Close()
		a.logger.Info("shadow cache stopped",
			slog.Uint64("shadow_dropped", a.shadow.Dropped()),
			slog.Uint64("shadow_skipped", a.shadow.Skipped()),
		)
	}
	a.emitter.Close()
	a.logger.Info("gateway stopped", slog.Uint64("telemetry_dropped", a.emitter.Dropped()))
}

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	bucketTTLFloor    = time.Minute
)

// redisOptions leaves the time budget to the callers' contexts: no internal
// retries, and reads and writes end at the context deadline.
func redisOptions(addr string) *redis.Options {
	return &redis.Options{Addr: addr, MaxRetries: -1, ContextTimeoutEnabled: true}
}

func newEmbedder(cfg config.Config) embed.Embedder {
	if cfg.EmbedURL == "" {
		return embed.Local{}
	}
	return embed.NewHTTP("openai", cfg.EmbedURL, cfg.EmbedModel, cfg.EmbedKey, cfg.UpstreamTimeout)
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout}
}
