// Package config loads gateway configuration from PUSAQ_* environment
// variables with explicit defaults and validation. Cloud Run injects env
// vars natively and Compose sets them locally, so no config files or
// libraries are needed.
package config

import (
	"fmt"
	"strconv"
	"time"
)

// Defaults applied when the corresponding variable is unset.
const (
	DefaultAddr            = ":8080"
	DefaultDrainDelay      = 1 * time.Second
	DefaultShutdownGrace   = 10 * time.Second
	DefaultTelemetryBuffer = 1024
)

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
}

// Load reads configuration through lookup (os.LookupEnv in production,
// a map lookup in tests), applying defaults and validating values.
func Load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Addr:            DefaultAddr,
		DrainDelay:      DefaultDrainDelay,
		ShutdownGrace:   DefaultShutdownGrace,
		TelemetryBuffer: DefaultTelemetryBuffer,
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

	return cfg, nil
}
