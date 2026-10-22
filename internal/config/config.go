// Package config loads gateway configuration from PUSAQ_* environment
// variables with explicit defaults and validation. Cloud Run injects env
// vars natively and Compose sets them locally, so no config files or
// libraries are needed.
package config

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aalvaropc/pusaq/internal/breaker"
	"github.com/aalvaropc/pusaq/internal/core"
)

// Defaults applied when the corresponding variable is unset.
const (
	DefaultAddr             = ":8080"
	DefaultDrainDelay       = 1 * time.Second
	DefaultShutdownGrace    = 10 * time.Second
	DefaultTelemetryBuffer  = 1024
	DefaultUpstreamTimeout  = 30 * time.Second
	DefaultBodyReadTimeout  = 10 * time.Second
	DefaultWriteIdleTimeout = 30 * time.Second
	DefaultRPMLimit         = 60.0
	DefaultTPMLimit         = 100000.0
	DefaultTPMReserve       = 512
	DefaultBucketTTL        = 10 * time.Minute
	DefaultCacheTau         = 0.95
	DefaultCacheMaxEntries  = 10000
	DefaultLeaseSize        = 10
	DefaultLeaseTTL         = 1 * time.Second
)

// MinAdminTokenLen is the shortest PUSAQ_ADMIN_TOKEN accepted: the token
// guards failure injection on a reachable service, so it must not be trivial.
const MinAdminTokenLen = 16

// regionLabel is the shape PUSAQ_REGION and routing-rule tags must have:
// header-safe and log-safe.
var regionLabel = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Rate-limit modes.
const (
	RateLimitStrict = "strict"
	RateLimitLease  = "lease"
)

// Cache modes.
const (
	CacheOff    = "off"
	CacheShadow = "shadow"
)

// Route maps a model alias to a provider name and its physical upstream
// model. Physical model names live here — in configuration — only.
type Route struct {
	Provider string
	Model    string
}

// Price is what one physical model costs, in USD per million tokens
// (PUSAQ_PRICES).
type Price struct {
	InputPerM  float64
	OutputPerM float64
}

// TenantPolicy is what one tenant may do (PUSAQ_TENANT_POLICIES). A zero
// field restricts nothing.
type TenantPolicy struct {
	// Models lists the concrete aliases the tenant may be served by; nil
	// allows every alias.
	Models []string
	// MaxPromptChars caps message content bytes; zero means no cap.
	MaxPromptChars int
	// BudgetUSD is the monthly spend ceiling; zero means no budget.
	BudgetUSD float64
	// FailClosed rejects requests when a limit's store is unreachable.
	FailClosed bool
	// Sensitive and Injection choose what the content rules do when they
	// match: "log" (default: flag the cost event) or "block" (403).
	Sensitive string
	Injection string
	// CacheOff keeps every request of the tenant out of the semantic cache.
	CacheOff bool
}

// Upstream names one OpenAI-compatible backend. APIKey, when set
// (PUSAQ_UPSTREAM_KEY_<NAME>), authenticates calls to it; it is never
// logged.
type Upstream struct {
	Name   string
	URL    string
	APIKey string
}

// envKeyName maps an upstream name to its key variable suffix: uppercased,
// non-alphanumerics replaced by underscores.
func envKeyName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return "PUSAQ_UPSTREAM_KEY_" + b.String()
}

func defaultUpstreams() []Upstream {
	return []Upstream{{Name: "mock", URL: "http://localhost:9090/v1"}}
}

// AutoAlias is the router alias: it has no physical model of its own and
// resolves to a concrete alias through the routing rules.
const AutoAlias = "pusaq/auto"

// DefaultRoutingTarget is the alias an auto request falls back to when no
// rule names its feature tag (and when it carries none).
const DefaultRoutingTarget = "pusaq/fast"

// defaultRoutes maps the concrete aliases to the mock model; physical
// models are overridden per alias via PUSAQ_ROUTES. pusaq/auto is not a
// route: it resolves through PUSAQ_ROUTING_RULES.
func defaultRoutes() map[string]Route {
	return map[string]Route{
		"pusaq/fast": {Provider: "mock", Model: "mock-model"},
		"pusaq/best": {Provider: "mock", Model: "mock-model"},
	}
}

// defaultRoutingRules is the explicit table docs/design.md §5.5 describes:
// cheap work to the fast model, reasoning and code to the best one.
func defaultRoutingRules() map[string]string {
	return map[string]string{
		"classify": "pusaq/fast",
		"extract":  "pusaq/fast",
		"reason":   "pusaq/best",
		"code":     "pusaq/best",
	}
}

// Config holds the gateway's runtime settings.
type Config struct {
	BodyReadTimeout  time.Duration
	WriteIdleTimeout time.Duration
	// Addr is the listen address (PUSAQ_ADDR).
	Addr string
	// DrainDelay is how long the gateway keeps serving after turning
	// not-ready on SIGTERM/SIGINT, so load-balancer probes observe the 503
	// before the listener closes (PUSAQ_DRAIN_DELAY, Go duration syntax).
	DrainDelay time.Duration
	// ShutdownGrace bounds how long draining waits for in-flight requests
	// after the drain delay (PUSAQ_SHUTDOWN_GRACE, Go duration syntax).
	ShutdownGrace time.Duration
	// TelemetryBuffer is the cost-event buffer capacity
	// (PUSAQ_TELEMETRY_BUFFER); overflow drops events.
	TelemetryBuffer int
	// Region labels this replica's region in cost events and in the
	// X-Pusaq-Region response header (PUSAQ_REGION); empty means unlabeled.
	Region string
	// AdminToken enables the admin endpoints that failure-injection
	// experiments use, as the bearer token they require (PUSAQ_ADMIN_TOKEN);
	// empty means the endpoints are not served at all.
	AdminToken string
	// Upstreams are the OpenAI-compatible backends in priority order:
	// the first is the primary for all aliases, the rest are fallbacks
	// (PUSAQ_UPSTREAMS, "name=url" comma-separated).
	Upstreams []Upstream
	// UpstreamTimeout bounds every provider call (PUSAQ_UPSTREAM_TIMEOUT).
	UpstreamTimeout time.Duration
	// Routes maps the concrete model aliases to providers and physical
	// models. pusaq/auto is never a route.
	Routes map[string]Route
	// RoutingRules maps a request's feature tag to a concrete alias for
	// pusaq/auto (PUSAQ_ROUTING_RULES); RoutingDefault is the alias used
	// when the tag is absent or unknown.
	RoutingRules   map[string]string
	RoutingDefault string
	// Prices is the price table keyed by physical model (PUSAQ_PRICES);
	// nil means no table: cost events carry no cost fields.
	// PriceTableVersion labels the table (PUSAQ_PRICE_TABLE_VERSION) and
	// is required whenever a table is set.
	Prices            map[string]Price
	PriceTableVersion string
	// TenantPolicies holds per-tenant policies keyed by tenant
	// (PUSAQ_TENANT_POLICIES); tenants absent here are unrestricted.
	TenantPolicies map[string]TenantPolicy
	APIKeys        map[string]string
	DevMode        bool
	// RedisAddr enables distributed rate limiting when set
	// (PUSAQ_REDIS_ADDR); unset means limiting is disabled, announced by
	// a startup warning.
	RedisAddr string
	// RPMLimit is the sustained per-tenant request rate in requests per
	// minute (PUSAQ_RPM_LIMIT).
	RPMLimit float64
	// RPMBurst is the per-tenant bucket capacity (PUSAQ_RPM_BURST,
	// default: equal to RPMLimit).
	RPMBurst float64
	// RateLimitMode is strict (default: one atomic store decision per
	// request) or lease (each replica takes batches of LeaseSize request
	// tokens and admits locally until they run out or LeaseTTL passes).
	// Lease mode applies to request limits only (PUSAQ_RATELIMIT_MODE).
	RateLimitMode string
	// LeaseSize is the number of request tokens a replica takes per store
	// round-trip in lease mode (PUSAQ_LEASE_SIZE); LeaseTTL is how long it
	// may hold them before unused tokens are returned (PUSAQ_LEASE_TTL).
	LeaseSize int
	LeaseTTL  time.Duration
	// Circuit-breaker parameters applied to every upstream backend
	// (PUSAQ_BREAKER_WINDOW, _MIN_SAMPLES, _THRESHOLD, _COOLDOWN, _PROBES).
	BreakerWindow     int
	BreakerMinSamples int
	BreakerThreshold  float64
	BreakerCooldown   time.Duration
	BreakerProbes     int
	// TPMLimit is the sustained per-tenant LLM-token rate in tokens per
	// minute (PUSAQ_TPM_LIMIT). Zero disables token limiting, announced by
	// a startup warning.
	TPMLimit float64
	// TPMBurst is the per-tenant token-bucket capacity (PUSAQ_TPM_BURST,
	// default: equal to TPMLimit).
	TPMBurst float64
	// TPMReserveDefault is the output reservation charged when a request
	// does not set max_tokens (PUSAQ_TPM_RESERVE_DEFAULT).
	TPMReserveDefault int
	// CacheMode is the semantic cache mode: off (default) or shadow
	// (PUSAQ_CACHE_MODE). Shadow never serves; it only measures.
	CacheMode string
	// CacheTau labels shadow records hit/miss (PUSAQ_CACHE_TAU); sweeps
	// of other thresholds happen offline from the records.
	CacheTau float64
	// CacheMaxEntries caps the shadow index (PUSAQ_CACHE_MAX_ENTRIES).
	CacheMaxEntries int
	// EmbedderKind is "local" or "openai" (PUSAQ_EMBEDDER, "local" or
	// "openai:<url>"); an openai embedder calls EmbedURL with EmbedModel
	// (PUSAQ_EMBED_MODEL) and the bearer key EmbedKey (PUSAQ_EMBED_KEY),
	// never logged.
	EmbedderKind string
	EmbedURL     string
	EmbedModel   string
	EmbedKey     string
}

// Load reads configuration through lookup (os.LookupEnv in production,
// a map lookup in tests), applying defaults and validating values.
func Load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Addr:             DefaultAddr,
		BodyReadTimeout:  DefaultBodyReadTimeout,
		WriteIdleTimeout: DefaultWriteIdleTimeout,
		DrainDelay:       DefaultDrainDelay,
		ShutdownGrace:    DefaultShutdownGrace,
		TelemetryBuffer:  DefaultTelemetryBuffer,
		Upstreams:        defaultUpstreams(),
		UpstreamTimeout:  DefaultUpstreamTimeout,
		Routes:           defaultRoutes(),
	}

	if v, ok := lookup("PUSAQ_ADDR"); ok {
		if v == "" {
			return Config{}, fmt.Errorf("PUSAQ_ADDR: must not be empty")
		}
		cfg.Addr = v
	}

	for _, setting := range []struct {
		name  string
		value *time.Duration
	}{
		{"PUSAQ_BODY_READ_TIMEOUT", &cfg.BodyReadTimeout},
		{"PUSAQ_WRITE_IDLE_TIMEOUT", &cfg.WriteIdleTimeout},
	} {
		if v, ok := lookup(setting.name); ok {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return Config{}, fmt.Errorf("%s: must be a positive duration", setting.name)
			}
			*setting.value = d
		}
	}

	if v, ok := lookup("PUSAQ_DRAIN_DELAY"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_DRAIN_DELAY: %w", err)
		}
		if d < 0 {
			return Config{}, fmt.Errorf("PUSAQ_DRAIN_DELAY: must be non-negative, got %s", d)
		}
		cfg.DrainDelay = d
	}

	if v, ok := lookup("PUSAQ_SHUTDOWN_GRACE"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_SHUTDOWN_GRACE: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_SHUTDOWN_GRACE: must be positive, got %s", d)
		}
		cfg.ShutdownGrace = d
	}

	if v, ok := lookup("PUSAQ_TELEMETRY_BUFFER"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_TELEMETRY_BUFFER: not an integer: %q", v)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_TELEMETRY_BUFFER: must be positive, got %d", n)
		}
		cfg.TelemetryBuffer = n
	}

	if v, ok := lookup("PUSAQ_REGION"); ok && v != "" {
		v = strings.TrimSpace(v)
		if !regionLabel.MatchString(v) {
			return Config{}, fmt.Errorf("PUSAQ_REGION: must be a label of letters, digits, '.', '_' or '-', got %q", v)
		}
		cfg.Region = v
	}

	if v, ok := lookup("PUSAQ_ADMIN_TOKEN"); ok && v != "" {
		if strings.TrimSpace(v) != v || strings.ContainsAny(v, " \t\r\n") {
			return Config{}, fmt.Errorf("PUSAQ_ADMIN_TOKEN: must not contain whitespace")
		}
		if len(v) < MinAdminTokenLen {
			return Config{}, fmt.Errorf("PUSAQ_ADMIN_TOKEN: must be at least %d characters", MinAdminTokenLen)
		}
		cfg.AdminToken = v
	}

	if v, ok := lookup("PUSAQ_UPSTREAMS"); ok && v != "" {
		ups, err := parseUpstreams(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_UPSTREAMS: %w", err)
		}
		cfg.Upstreams = ups
	}
	for i := range cfg.Upstreams {
		if key, ok := lookup(envKeyName(cfg.Upstreams[i].Name)); ok && key != "" {
			cfg.Upstreams[i].APIKey = key
		}
	}

	if v, ok := lookup("PUSAQ_ROUTES"); ok && v != "" {
		if err := applyRouteModels(cfg.Routes, v); err != nil {
			return Config{}, fmt.Errorf("PUSAQ_ROUTES: %w", err)
		}
	}
	cfg.RoutingRules = defaultRoutingRules()
	cfg.RoutingDefault = DefaultRoutingTarget
	if v, ok := lookup("PUSAQ_ROUTING_RULES"); ok && v != "" {
		rules, def, err := parseRoutingRules(v, cfg.Routes)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_ROUTING_RULES: %w", err)
		}
		cfg.RoutingRules, cfg.RoutingDefault = rules, def
	}

	if v, ok := lookup("PUSAQ_PRICES"); ok && v != "" {
		prices, err := parsePrices(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_PRICES: %w", err)
		}
		cfg.Prices = prices
	}
	if v, ok := lookup("PUSAQ_PRICE_TABLE_VERSION"); ok && v != "" {
		cfg.PriceTableVersion = v
	}
	if cfg.Prices != nil && cfg.PriceTableVersion == "" {
		return Config{}, fmt.Errorf("PUSAQ_PRICE_TABLE_VERSION: required when PUSAQ_PRICES is set")
	}
	if cfg.Prices == nil && cfg.PriceTableVersion != "" {
		return Config{}, fmt.Errorf("PUSAQ_PRICE_TABLE_VERSION: set without PUSAQ_PRICES")
	}
	if cfg.Prices != nil {
		for alias, r := range cfg.Routes {
			if _, priced := cfg.Prices[r.Model]; !priced {
				return Config{}, fmt.Errorf("PUSAQ_PRICES: no price for model %q (route %s)", r.Model, alias)
			}
		}
	}

	if v, ok := lookup("PUSAQ_UPSTREAM_TIMEOUT"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_UPSTREAM_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_UPSTREAM_TIMEOUT: must be positive, got %s", d)
		}
		cfg.UpstreamTimeout = d
	}

	cfg.RPMLimit = DefaultRPMLimit
	if v, ok := lookup("PUSAQ_REDIS_ADDR"); ok && v != "" {
		cfg.RedisAddr = v
	}
	if v, ok := lookup("PUSAQ_RPM_LIMIT"); ok {
		n, err := parseFinite(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_RPM_LIMIT: must be a positive number, got %q", v)
		}
		cfg.RPMLimit = n
	}
	cfg.RPMBurst = cfg.RPMLimit
	if v, ok := lookup("PUSAQ_RPM_BURST"); ok {
		n, err := parseFinite(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_RPM_BURST: must be a positive number, got %q", v)
		}
		cfg.RPMBurst = n
	}

	cfg.RateLimitMode = RateLimitStrict
	if v, ok := lookup("PUSAQ_RATELIMIT_MODE"); ok && v != "" {
		if v != RateLimitStrict && v != RateLimitLease {
			return Config{}, fmt.Errorf("PUSAQ_RATELIMIT_MODE: must be %q or %q, got %q", RateLimitStrict, RateLimitLease, v)
		}
		cfg.RateLimitMode = v
	}
	cfg.LeaseSize = DefaultLeaseSize
	if v, ok := lookup("PUSAQ_LEASE_SIZE"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_LEASE_SIZE: must be a positive integer, got %q", v)
		}
		cfg.LeaseSize = n
	}
	if cfg.RateLimitMode == RateLimitLease && float64(cfg.LeaseSize) > cfg.RPMBurst {
		return Config{}, fmt.Errorf("PUSAQ_LEASE_SIZE: %d exceeds the request bucket capacity %g", cfg.LeaseSize, cfg.RPMBurst)
	}
	cfg.LeaseTTL = DefaultLeaseTTL
	if v, ok := lookup("PUSAQ_LEASE_TTL"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_LEASE_TTL: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_LEASE_TTL: must be positive, got %s", d)
		}
		cfg.LeaseTTL = d
	}

	cfg.BreakerWindow = breaker.DefaultWindow
	cfg.BreakerMinSamples = breaker.DefaultMinSamples
	cfg.BreakerThreshold = breaker.DefaultFailureThreshold
	cfg.BreakerCooldown = breaker.DefaultCooldown
	cfg.BreakerProbes = breaker.DefaultProbes
	if v, ok := lookup("PUSAQ_BREAKER_WINDOW"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_BREAKER_WINDOW: must be a positive integer, got %q", v)
		}
		cfg.BreakerWindow = n
	}
	if v, ok := lookup("PUSAQ_BREAKER_MIN_SAMPLES"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_BREAKER_MIN_SAMPLES: must be a positive integer, got %q", v)
		}
		cfg.BreakerMinSamples = n
	}
	if cfg.BreakerMinSamples > cfg.BreakerWindow {
		return Config{}, fmt.Errorf("PUSAQ_BREAKER_MIN_SAMPLES: %d exceeds the window %d", cfg.BreakerMinSamples, cfg.BreakerWindow)
	}
	if v, ok := lookup("PUSAQ_BREAKER_THRESHOLD"); ok {
		n, err := parseFinite(v)
		if err != nil || n <= 0 || n >= 1 {
			return Config{}, fmt.Errorf("PUSAQ_BREAKER_THRESHOLD: must be in (0,1), got %q", v)
		}
		cfg.BreakerThreshold = n
	}
	if v, ok := lookup("PUSAQ_BREAKER_COOLDOWN"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_BREAKER_COOLDOWN: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_BREAKER_COOLDOWN: must be positive, got %s", d)
		}
		cfg.BreakerCooldown = d
	}
	if v, ok := lookup("PUSAQ_BREAKER_PROBES"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_BREAKER_PROBES: must be a positive integer, got %q", v)
		}
		cfg.BreakerProbes = n
	}

	cfg.TPMLimit = DefaultTPMLimit
	if v, ok := lookup("PUSAQ_TPM_LIMIT"); ok {
		n, err := parseFinite(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("PUSAQ_TPM_LIMIT: must be a non-negative number (0 disables token limiting), got %q", v)
		}
		cfg.TPMLimit = n
	}
	cfg.TPMBurst = cfg.TPMLimit
	// An empty value means "default", so an environment can declare the
	// variable without choosing a burst (Compose profiles do this).
	if v, ok := lookup("PUSAQ_TPM_BURST"); ok && v != "" {
		if cfg.TPMLimit == 0 {
			return Config{}, fmt.Errorf("PUSAQ_TPM_BURST: meaningless when PUSAQ_TPM_LIMIT is 0 (token limiting disabled)")
		}
		n, err := parseFinite(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_TPM_BURST: must be a positive number, got %q", v)
		}
		cfg.TPMBurst = n
	}
	cfg.TPMReserveDefault = DefaultTPMReserve
	if v, ok := lookup("PUSAQ_TPM_RESERVE_DEFAULT"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_TPM_RESERVE_DEFAULT: must be a positive integer, got %q", v)
		}
		cfg.TPMReserveDefault = n
	}
	if cfg.TPMLimit > 0 && float64(cfg.TPMReserveDefault) > cfg.TPMBurst {
		return Config{}, fmt.Errorf("PUSAQ_TPM_RESERVE_DEFAULT: %d exceeds the token bucket capacity %g", cfg.TPMReserveDefault, cfg.TPMBurst)
	}

	cfg.CacheMode = CacheOff
	if v, ok := lookup("PUSAQ_CACHE_MODE"); ok && v != "" {
		if v != CacheOff && v != CacheShadow {
			return Config{}, fmt.Errorf("PUSAQ_CACHE_MODE: must be %q or %q, got %q", CacheOff, CacheShadow, v)
		}
		cfg.CacheMode = v
	}
	cfg.CacheTau = DefaultCacheTau
	if v, ok := lookup("PUSAQ_CACHE_TAU"); ok {
		n, err := parseFinite(v)
		if err != nil || n <= 0 || n > 1 {
			return Config{}, fmt.Errorf("PUSAQ_CACHE_TAU: must be in (0,1], got %q", v)
		}
		cfg.CacheTau = n
	}
	cfg.CacheMaxEntries = DefaultCacheMaxEntries
	if v, ok := lookup("PUSAQ_CACHE_MAX_ENTRIES"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_CACHE_MAX_ENTRIES: must be a positive integer, got %q", v)
		}
		cfg.CacheMaxEntries = n
	}
	cfg.EmbedderKind = "local"
	if v, ok := lookup("PUSAQ_EMBEDDER"); ok && v != "" && v != "local" {
		rawURL, found := strings.CutPrefix(v, "openai:")
		u, err := url.Parse(rawURL)
		if !found || err != nil || u.Scheme == "" || u.Host == "" {
			return Config{}, fmt.Errorf("PUSAQ_EMBEDDER: must be \"local\" or \"openai:<absolute url>\"")
		}
		cfg.EmbedderKind, cfg.EmbedURL = "openai", strings.TrimRight(rawURL, "/")
	}
	if v, ok := lookup("PUSAQ_EMBED_MODEL"); ok {
		cfg.EmbedModel = v
	}
	if v, ok := lookup("PUSAQ_EMBED_KEY"); ok {
		cfg.EmbedKey = v
	}
	if cfg.EmbedURL != "" && cfg.EmbedModel == "" {
		return Config{}, fmt.Errorf("PUSAQ_EMBED_MODEL: required with an openai embedder")
	}
	if cfg.EmbedURL == "" && cfg.EmbedKey != "" {
		return Config{}, fmt.Errorf("PUSAQ_EMBED_KEY: set without an openai embedder")
	}

	if v, ok := lookup("PUSAQ_DEV_MODE"); ok {
		mode, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_DEV_MODE: must be true or false")
		}
		cfg.DevMode = mode
	}

	if v, ok := lookup("PUSAQ_API_KEYS"); ok {
		keys, err := parseAPIKeys(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_API_KEYS: %w", err)
		}
		cfg.APIKeys = keys
	}

	if cfg.DevMode && len(cfg.APIKeys) > 0 {
		return Config{}, fmt.Errorf("PUSAQ_DEV_MODE: cannot be combined with PUSAQ_API_KEYS")
	}
	if !cfg.DevMode && len(cfg.APIKeys) == 0 {
		return Config{}, fmt.Errorf("PUSAQ_API_KEYS: required unless PUSAQ_DEV_MODE=true")
	}

	if v, ok := lookup("PUSAQ_TENANT_POLICIES"); ok && v != "" {
		policies, err := parseTenantPolicies(v, cfg.Routes)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_TENANT_POLICIES: %w", err)
		}
		for tenant, p := range policies {
			if !cfg.knowsTenant(tenant) {
				return Config{}, fmt.Errorf("PUSAQ_TENANT_POLICIES: tenant %q is not named by PUSAQ_API_KEYS", tenant)
			}
			if p.BudgetUSD > 0 {
				if cfg.Prices == nil {
					return Config{}, fmt.Errorf("PUSAQ_TENANT_POLICIES: tenant %q: budget_usd requires PUSAQ_PRICES", tenant)
				}
				if cfg.RedisAddr == "" {
					return Config{}, fmt.Errorf("PUSAQ_TENANT_POLICIES: tenant %q: budget_usd requires PUSAQ_REDIS_ADDR", tenant)
				}
			}
			if p.FailClosed && cfg.RedisAddr == "" {
				return Config{}, fmt.Errorf("PUSAQ_TENANT_POLICIES: tenant %q: fail_closed requires PUSAQ_REDIS_ADDR", tenant)
			}
		}
		cfg.TenantPolicies = policies
	}

	return cfg, nil
}

// knowsTenant reports whether a tenant can ever be attributed: one named
// by an API key, or the dev tenant when no keys are configured.
func (c Config) knowsTenant(tenant string) bool {
	if len(c.APIKeys) == 0 {
		return tenant == core.DevTenant
	}
	for _, t := range c.APIKeys {
		if t == tenant {
			return true
		}
	}
	return false
}

// HasBudgets reports whether any tenant policy sets a monthly budget.
func (c Config) HasBudgets() bool {
	for _, p := range c.TenantPolicies {
		if p.BudgetUSD > 0 {
			return true
		}
	}
	return false
}

// parseTenantPolicies parses "tenant:key=value,...;tenant2:..." — tenants
// separated by ';', fields by ',', list values by '|'.
func parseTenantPolicies(v string, routes map[string]Route) (map[string]TenantPolicy, error) {
	out := make(map[string]TenantPolicy)
	for entry := range strings.SplitSeq(v, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		tenant, fields, ok := strings.Cut(entry, ":")
		tenant = strings.TrimSpace(tenant)
		if !ok || tenant == "" || strings.TrimSpace(fields) == "" {
			return nil, fmt.Errorf("malformed entry %q, want tenant:key=value pairs", entry)
		}
		if _, dup := out[tenant]; dup {
			return nil, fmt.Errorf("duplicate policy for tenant %q", tenant)
		}
		p, err := parsePolicyFields(tenant, fields, routes)
		if err != nil {
			return nil, err
		}
		out[tenant] = p
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid tenant:key=value entries")
	}
	return out, nil
}

func parsePolicyFields(tenant, fields string, routes map[string]Route) (TenantPolicy, error) {
	var p TenantPolicy
	seen := make(map[string]bool)
	for f := range strings.SplitSeq(fields, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		key, val, ok := strings.Cut(f, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !ok || key == "" || val == "" {
			return p, fmt.Errorf("tenant %q: malformed field %q, want key=value", tenant, f)
		}
		if seen[key] {
			return p, fmt.Errorf("tenant %q: duplicate field %q", tenant, key)
		}
		seen[key] = true
		switch key {
		case "models":
			for a := range strings.SplitSeq(val, "|") {
				a = strings.TrimSpace(a)
				if a == "" {
					return p, fmt.Errorf("tenant %q: empty alias in models", tenant)
				}
				if a == AutoAlias {
					return p, fmt.Errorf("tenant %q: models must list concrete aliases; %q resolves through PUSAQ_ROUTING_RULES", tenant, a)
				}
				if _, exists := routes[a]; !exists {
					return p, fmt.Errorf("tenant %q: unknown alias %q", tenant, a)
				}
				for _, prev := range p.Models {
					if prev == a {
						return p, fmt.Errorf("tenant %q: duplicate alias %q", tenant, a)
					}
				}
				p.Models = append(p.Models, a)
			}
		case "max_prompt_chars":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return p, fmt.Errorf("tenant %q: max_prompt_chars must be a positive integer, got %q", tenant, val)
			}
			p.MaxPromptChars = n
		case "budget_usd":
			n, err := parseFinite(val)
			if err != nil || n <= 0 {
				return p, fmt.Errorf("tenant %q: budget_usd must be a positive number, got %q", tenant, val)
			}
			p.BudgetUSD = n
		case "fail_closed":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return p, fmt.Errorf("tenant %q: fail_closed must be true or false, got %q", tenant, val)
			}
			p.FailClosed = b
		case "sensitive", "injection":
			if val != "log" && val != "block" {
				return p, fmt.Errorf("tenant %q: %s must be log or block, got %q", tenant, key, val)
			}
			if key == "sensitive" {
				p.Sensitive = val
			} else {
				p.Injection = val
			}
		case "cache":
			if val != "on" && val != "off" {
				return p, fmt.Errorf("tenant %q: cache must be on or off, got %q", tenant, val)
			}
			p.CacheOff = val == "off"
		default:
			return p, fmt.Errorf("tenant %q: unknown field %q (want models, max_prompt_chars, budget_usd, fail_closed, sensitive, injection, cache)", tenant, key)
		}
	}
	if len(seen) == 0 {
		return p, fmt.Errorf("tenant %q: no fields", tenant)
	}
	return p, nil
}

// parseUpstreams parses "name=url" comma-separated backends in priority
// order, failing fast on malformed pairs, invalid URLs, duplicates, and
// names that share a key variable.
func parseUpstreams(v string) ([]Upstream, error) {
	var ups []Upstream
	keyVars := make(map[string]string)
	position := 0
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		position++
		name, rawURL, ok := strings.Cut(pair, "=")
		name, rawURL = strings.TrimSpace(name), strings.TrimSpace(rawURL)
		if !ok || name == "" || rawURL == "" {
			return nil, fmt.Errorf("pair %d is malformed, want name=url", position)
		}
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("upstream %q: not an absolute URL", name)
		}
		keyVar := envKeyName(name)
		if other, taken := keyVars[keyVar]; taken {
			if other == name {
				return nil, fmt.Errorf("duplicate upstream name %q", name)
			}
			return nil, fmt.Errorf("upstreams %q and %q share %s", other, name, keyVar)
		}
		keyVars[keyVar] = name
		ups = append(ups, Upstream{Name: name, URL: strings.TrimRight(rawURL, "/")})
	}
	if len(ups) == 0 {
		return nil, fmt.Errorf("no valid name=url pairs")
	}
	return ups, nil
}

// parsePrices parses "model=input/output" comma-separated rates in USD per
// million tokens, failing fast on malformed pairs, negative or non-finite
// rates, and duplicates.
func parsePrices(v string) (map[string]Price, error) {
	prices := make(map[string]Price)
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		model, rates, ok := strings.Cut(pair, "=")
		if !ok || model == "" || rates == "" {
			return nil, fmt.Errorf("malformed pair %q, want model=input/output", pair)
		}
		in, out, ok := strings.Cut(rates, "/")
		if !ok {
			return nil, fmt.Errorf("malformed pair %q, want model=input/output", pair)
		}
		inputPerM, err := parseFinite(in)
		if err != nil || inputPerM < 0 {
			return nil, fmt.Errorf("model %q: input rate must be a non-negative number, got %q", model, in)
		}
		outputPerM, err := parseFinite(out)
		if err != nil || outputPerM < 0 {
			return nil, fmt.Errorf("model %q: output rate must be a non-negative number, got %q", model, out)
		}
		if _, dup := prices[model]; dup {
			return nil, fmt.Errorf("duplicate price for model %q", model)
		}
		prices[model] = Price{InputPerM: inputPerM, OutputPerM: outputPerM}
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no valid model=input/output pairs")
	}
	return prices, nil
}

// applyRouteModels overrides per-alias physical models from
// "alias=model" comma-separated pairs. The alias set is fixed — the three
// public aliases are the product contract — so an unknown alias is a
// startup error, not a new route.
func applyRouteModels(routes map[string]Route, v string) error {
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		alias, model, ok := strings.Cut(pair, "=")
		if !ok || alias == "" || model == "" {
			return fmt.Errorf("malformed pair %q, want alias=model", pair)
		}
		if alias == AutoAlias {
			return fmt.Errorf("alias %q has no physical model; it resolves through PUSAQ_ROUTING_RULES", alias)
		}
		r, exists := routes[alias]
		if !exists {
			return fmt.Errorf("unknown alias %q", alias)
		}
		r.Model = model
		routes[alias] = r
	}
	return nil
}

// parseRoutingRules parses "tag=alias" comma-separated rules for the auto
// alias. The whole table is replaced: a "default" entry is required, every
// target must be a concrete configured alias, and tags must be unique.
func parseFinite(s string) (float64, error) {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%q is not a finite number", s)
	}
	return n, nil
}

func parseRoutingRules(v string, routes map[string]Route) (map[string]string, string, error) {
	rules := make(map[string]string)
	def := ""
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		tag, alias, ok := strings.Cut(pair, "=")
		if !ok || tag == "" || alias == "" {
			return nil, "", fmt.Errorf("malformed pair %q, want tag=alias", pair)
		}
		if !regionLabel.MatchString(tag) {
			return nil, "", fmt.Errorf("rule %q: tags use letters, digits, '.', '_' and '-'", tag)
		}
		if alias == AutoAlias {
			return nil, "", fmt.Errorf("rule %q: target must be a concrete alias, not %q", tag, alias)
		}
		if _, exists := routes[alias]; !exists {
			return nil, "", fmt.Errorf("rule %q: unknown alias %q", tag, alias)
		}
		if tag == "default" {
			if def != "" {
				return nil, "", fmt.Errorf("duplicate default rule")
			}
			def = alias
			continue
		}
		if _, dup := rules[tag]; dup {
			return nil, "", fmt.Errorf("duplicate rule for tag %q", tag)
		}
		rules[tag] = alias
	}
	if def == "" {
		return nil, "", fmt.Errorf("a default rule is required (default=<alias>)")
	}
	return rules, def, nil
}

// parseAPIKeys parses "key:tenant" comma-separated pairs, failing fast on
// malformed pairs, empty parts, and duplicate keys — config bugs surface at
// startup, not at request time.
func parseAPIKeys(v string) (map[string]string, error) {
	keys := make(map[string]string)
	position := 0
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		position++
		key, tenant, ok := strings.Cut(pair, ":")
		key, tenant = strings.TrimSpace(key), strings.TrimSpace(tenant)
		if !ok || key == "" || tenant == "" {
			return nil, fmt.Errorf("pair %d is malformed, want key:tenant", position)
		}
		if other, dup := keys[key]; dup {
			return nil, fmt.Errorf("duplicate API key for tenants %q and %q", other, tenant)
		}
		keys[key] = tenant
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid key:tenant pairs")
	}
	return keys, nil
}
