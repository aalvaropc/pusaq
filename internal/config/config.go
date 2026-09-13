// Package config loads gateway configuration from PUSAQ_* environment
// variables with explicit defaults and validation. Cloud Run injects env
// vars natively and Compose sets them locally, so no config files or
// libraries are needed.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Defaults applied when the corresponding variable is unset.
const (
	DefaultAddr            = ":8080"
	DefaultDrainDelay      = 1 * time.Second
	DefaultShutdownGrace   = 10 * time.Second
	DefaultTelemetryBuffer = 1024
	DefaultUpstreamTimeout = 30 * time.Second
	DefaultRPMLimit        = 60.0
	DefaultTPMLimit        = 100000.0
	DefaultTPMReserve      = 512
	DefaultBucketTTL       = 10 * time.Minute
	DefaultCacheTau        = 0.95
	DefaultCacheMaxEntries = 10000
	DefaultLeaseSize       = 10
	DefaultLeaseTTL        = 1 * time.Second
)

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
	return []Upstream{{Name: "mock", URL: "http://localhost:9090"}}
}

// defaultRoutes maps the public aliases to the mock model; physical models
// are overridden per alias via PUSAQ_ROUTES. pusaq/auto currently resolves
// like pusaq/fast: routing rules are not implemented.
func defaultRoutes() map[string]Route {
	return map[string]Route{
		"pusaq/fast": {Provider: "mock", Model: "mock-model"},
		"pusaq/best": {Provider: "mock", Model: "mock-model"},
		"pusaq/auto": {Provider: "mock", Model: "mock-model"},
	}
}

// Config holds the gateway's runtime settings.
type Config struct {
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
	// Upstreams are the OpenAI-compatible backends in priority order:
	// the first is the primary for all aliases, the rest are fallbacks
	// (PUSAQ_UPSTREAMS, "name=url" comma-separated).
	Upstreams []Upstream
	// UpstreamTimeout bounds every provider call (PUSAQ_UPSTREAM_TIMEOUT).
	UpstreamTimeout time.Duration
	// Routes maps model aliases to providers and physical models.
	Routes map[string]Route
	// APIKeys maps API keys to tenant names (PUSAQ_API_KEYS,
	// "key:tenant" comma-separated). Empty means dev mode: no
	// authentication, every request attributed to the "dev" tenant.
	APIKeys map[string]string
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
	// EmbedderKind is "local" or "openai:<upstream>" naming a configured
	// upstream (PUSAQ_EMBEDDER); EmbedModel names the embedding model for
	// the openai kind (PUSAQ_EMBED_MODEL).
	EmbedderKind string
	EmbedModel   string
}

// Load reads configuration through lookup (os.LookupEnv in production,
// a map lookup in tests), applying defaults and validating values.
func Load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Addr:            DefaultAddr,
		DrainDelay:      DefaultDrainDelay,
		ShutdownGrace:   DefaultShutdownGrace,
		TelemetryBuffer: DefaultTelemetryBuffer,
		Upstreams:       defaultUpstreams(),
		UpstreamTimeout: DefaultUpstreamTimeout,
		Routes:          defaultRoutes(),
	}

	if v, ok := lookup("PUSAQ_ADDR"); ok {
		if v == "" {
			return Config{}, fmt.Errorf("PUSAQ_ADDR: must not be empty")
		}
		cfg.Addr = v
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
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("PUSAQ_RPM_LIMIT: must be a positive number, got %q", v)
		}
		cfg.RPMLimit = n
	}
	cfg.RPMBurst = cfg.RPMLimit
	if v, ok := lookup("PUSAQ_RPM_BURST"); ok {
		n, err := strconv.ParseFloat(v, 64)
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
	if float64(cfg.LeaseSize) > cfg.RPMBurst {
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

	cfg.TPMLimit = DefaultTPMLimit
	if v, ok := lookup("PUSAQ_TPM_LIMIT"); ok {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("PUSAQ_TPM_LIMIT: must be a non-negative number (0 disables token limiting), got %q", v)
		}
		cfg.TPMLimit = n
	}
	cfg.TPMBurst = cfg.TPMLimit
	if v, ok := lookup("PUSAQ_TPM_BURST"); ok {
		if cfg.TPMLimit == 0 {
			return Config{}, fmt.Errorf("PUSAQ_TPM_BURST: meaningless when PUSAQ_TPM_LIMIT is 0 (token limiting disabled)")
		}
		n, err := strconv.ParseFloat(v, 64)
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

	cfg.CacheMode = CacheOff
	if v, ok := lookup("PUSAQ_CACHE_MODE"); ok && v != "" {
		if v != CacheOff && v != CacheShadow {
			return Config{}, fmt.Errorf("PUSAQ_CACHE_MODE: must be %q or %q, got %q", CacheOff, CacheShadow, v)
		}
		cfg.CacheMode = v
	}
	cfg.CacheTau = DefaultCacheTau
	if v, ok := lookup("PUSAQ_CACHE_TAU"); ok {
		n, err := strconv.ParseFloat(v, 64)
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
	if v, ok := lookup("PUSAQ_EMBEDDER"); ok && v != "" {
		if v != "local" {
			name, found := strings.CutPrefix(v, "openai:")
			if !found || name == "" {
				return Config{}, fmt.Errorf("PUSAQ_EMBEDDER: must be \"local\" or \"openai:<upstream>\", got %q", v)
			}
			exists := false
			for _, up := range cfg.Upstreams {
				if up.Name == name {
					exists = true
				}
			}
			if !exists {
				return Config{}, fmt.Errorf("PUSAQ_EMBEDDER: upstream %q is not configured", name)
			}
		}
		cfg.EmbedderKind = v
	}
	if v, ok := lookup("PUSAQ_EMBED_MODEL"); ok {
		cfg.EmbedModel = v
	}
	if strings.HasPrefix(cfg.EmbedderKind, "openai:") && cfg.EmbedModel == "" {
		return Config{}, fmt.Errorf("PUSAQ_EMBED_MODEL: required when PUSAQ_EMBEDDER is %q", cfg.EmbedderKind)
	}

	if v, ok := lookup("PUSAQ_API_KEYS"); ok && v != "" {
		keys, err := parseAPIKeys(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_API_KEYS: %w", err)
		}
		cfg.APIKeys = keys
	}

	return cfg, nil
}

// parseUpstreams parses "name=url" comma-separated backends in priority
// order, failing fast on malformed pairs, invalid URLs, and duplicates.
func parseUpstreams(v string) ([]Upstream, error) {
	var ups []Upstream
	seen := make(map[string]bool)
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, rawURL, ok := strings.Cut(pair, "=")
		if !ok || name == "" || rawURL == "" {
			return nil, fmt.Errorf("malformed pair %q, want name=url", pair)
		}
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("upstream %q: not an absolute URL: %q", name, rawURL)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate upstream name %q", name)
		}
		seen[name] = true
		ups = append(ups, Upstream{Name: name, URL: strings.TrimRight(rawURL, "/")})
	}
	if len(ups) == 0 {
		return nil, fmt.Errorf("no valid name=url pairs")
	}
	return ups, nil
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
		r, exists := routes[alias]
		if !exists {
			return fmt.Errorf("unknown alias %q", alias)
		}
		r.Model = model
		routes[alias] = r
	}
	return nil
}

// parseAPIKeys parses "key:tenant" comma-separated pairs, failing fast on
// malformed pairs, empty parts, and duplicate keys — config bugs surface at
// startup, not at request time.
func parseAPIKeys(v string) (map[string]string, error) {
	keys := make(map[string]string)
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, tenant, ok := strings.Cut(pair, ":")
		if !ok || key == "" || tenant == "" {
			return nil, fmt.Errorf("malformed pair %q, want key:tenant", pair)
		}
		if _, dup := keys[key]; dup {
			return nil, fmt.Errorf("duplicate API key in pair %q", pair)
		}
		keys[key] = tenant
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid key:tenant pairs")
	}
	return keys, nil
}
