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
	DefaultMockURL         = "http://localhost:9090"
	DefaultUpstreamTimeout = 30 * time.Second
)

// Route maps a model alias to a provider name and its physical upstream
// model. Physical model names live here — in configuration — only.
type Route struct {
	Provider string
	Model    string
}

// defaultRoutes maps the public aliases to the mock provider until real
// adapters land. pusaq/auto currently resolves like pusaq/fast; the router
// slice will make it a real decision.
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
	// MockURL is the mock provider's base URL (PUSAQ_MOCK_URL).
	MockURL string
	// UpstreamTimeout bounds every provider call (PUSAQ_UPSTREAM_TIMEOUT).
	UpstreamTimeout time.Duration
	// Routes maps model aliases to providers and physical models.
	Routes map[string]Route
	// APIKeys maps API keys to tenant names (PUSAQ_API_KEYS,
	// "key:tenant" comma-separated). Empty means dev mode: no
	// authentication, every request attributed to the "dev" tenant.
	APIKeys map[string]string
}

// Load reads configuration through lookup (os.LookupEnv in production,
// a map lookup in tests), applying defaults and validating values.
func Load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Addr:            DefaultAddr,
		DrainDelay:      DefaultDrainDelay,
		ShutdownGrace:   DefaultShutdownGrace,
		TelemetryBuffer: DefaultTelemetryBuffer,
		MockURL:         DefaultMockURL,
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

	if v, ok := lookup("PUSAQ_MOCK_URL"); ok {
		u, err := url.Parse(v)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return Config{}, fmt.Errorf("PUSAQ_MOCK_URL: not an absolute URL: %q", v)
		}
		cfg.MockURL = strings.TrimRight(v, "/")
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

	if v, ok := lookup("PUSAQ_API_KEYS"); ok && v != "" {
		keys, err := parseAPIKeys(v)
		if err != nil {
			return Config{}, fmt.Errorf("PUSAQ_API_KEYS: %w", err)
		}
		cfg.APIKeys = keys
	}

	return cfg, nil
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
