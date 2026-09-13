package config

import (
	"reflect"
	"testing"
	"time"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults when nothing is set",
			env:  map[string]string{},
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024, UpstreamTimeout: 30 * time.Second, RPMLimit: 60, RPMBurst: 60, RateLimitMode: "strict", LeaseSize: 10, LeaseTTL: time.Second, TPMLimit: 100000, TPMBurst: 100000, TPMReserveDefault: 512, CacheMode: "off", CacheTau: 0.95, CacheMaxEntries: 10000, EmbedderKind: "local"},
		},
		{
			name: "all overrides applied",
			env: map[string]string{
				"PUSAQ_ADDR":                ":9091",
				"PUSAQ_DRAIN_DELAY":         "0s",
				"PUSAQ_SHUTDOWN_GRACE":      "30s",
				"PUSAQ_TELEMETRY_BUFFER":    "256",
				"PUSAQ_UPSTREAM_TIMEOUT":    "5s",
				"PUSAQ_REDIS_ADDR":          "redis:6379",
				"PUSAQ_RPM_LIMIT":           "120",
				"PUSAQ_RPM_BURST":           "30",
				"PUSAQ_RATELIMIT_MODE":      "lease",
				"PUSAQ_LEASE_SIZE":          "5",
				"PUSAQ_LEASE_TTL":           "500ms",
				"PUSAQ_TPM_LIMIT":           "6000",
				"PUSAQ_TPM_BURST":           "2000",
				"PUSAQ_TPM_RESERVE_DEFAULT": "256",
				"PUSAQ_CACHE_MODE":          "shadow",
				"PUSAQ_CACHE_TAU":           "0.9",
				"PUSAQ_CACHE_MAX_ENTRIES":   "500",
			},
			want: Config{Addr: ":9091", DrainDelay: 0, ShutdownGrace: 30 * time.Second, TelemetryBuffer: 256, UpstreamTimeout: 5 * time.Second, RedisAddr: "redis:6379", RPMLimit: 120, RPMBurst: 30, RateLimitMode: "lease", LeaseSize: 5, LeaseTTL: 500 * time.Millisecond, TPMLimit: 6000, TPMBurst: 2000, TPMReserveDefault: 256, CacheMode: "shadow", CacheTau: 0.9, CacheMaxEntries: 500, EmbedderKind: "local"},
		},
		{
			name:    "empty addr rejected",
			env:     map[string]string{"PUSAQ_ADDR": ""},
			wantErr: true,
		},
		{
			name:    "negative drain delay rejected",
			env:     map[string]string{"PUSAQ_DRAIN_DELAY": "-1s"},
			wantErr: true,
		},
		{
			name:    "malformed grace rejected",
			env:     map[string]string{"PUSAQ_SHUTDOWN_GRACE": "10 seconds"},
			wantErr: true,
		},
		{
			name:    "non-positive grace rejected",
			env:     map[string]string{"PUSAQ_SHUTDOWN_GRACE": "0s"},
			wantErr: true,
		},
		{
			name:    "non-integer buffer rejected",
			env:     map[string]string{"PUSAQ_TELEMETRY_BUFFER": "12abc"},
			wantErr: true,
		},
		{
			name:    "non-positive buffer rejected",
			env:     map[string]string{"PUSAQ_TELEMETRY_BUFFER": "-1"},
			wantErr: true,
		},
		{
			name:    "relative upstream URL rejected",
			env:     map[string]string{"PUSAQ_UPSTREAMS": "mock=localhost:9090"},
			wantErr: true,
		},
		{
			name:    "malformed upstream pair rejected",
			env:     map[string]string{"PUSAQ_UPSTREAMS": "http://x:1"},
			wantErr: true,
		},
		{
			name:    "duplicate upstream name rejected",
			env:     map[string]string{"PUSAQ_UPSTREAMS": "a=http://x:1,a=http://y:2"},
			wantErr: true,
		},
		{
			name:    "non-positive upstream timeout rejected",
			env:     map[string]string{"PUSAQ_UPSTREAM_TIMEOUT": "0s"},
			wantErr: true,
		},
		{
			name:    "non-positive rpm limit rejected",
			env:     map[string]string{"PUSAQ_RPM_LIMIT": "0"},
			wantErr: true,
		},
		{
			name:    "malformed rpm burst rejected",
			env:     map[string]string{"PUSAQ_RPM_BURST": "lots"},
			wantErr: true,
		},
		{
			name:    "unknown rate-limit mode rejected",
			env:     map[string]string{"PUSAQ_RATELIMIT_MODE": "batched"},
			wantErr: true,
		},
		{
			name:    "lease size above burst rejected",
			env:     map[string]string{"PUSAQ_RPM_BURST": "5", "PUSAQ_LEASE_SIZE": "6"},
			wantErr: true,
		},
		{
			name:    "non-positive lease ttl rejected",
			env:     map[string]string{"PUSAQ_LEASE_TTL": "0s"},
			wantErr: true,
		},
		{
			name:    "negative tpm limit rejected",
			env:     map[string]string{"PUSAQ_TPM_LIMIT": "-5"},
			wantErr: true,
		},
		{
			name: "zero tpm limit disables token limiting",
			env:  map[string]string{"PUSAQ_TPM_LIMIT": "0"},
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024, UpstreamTimeout: 30 * time.Second, RPMLimit: 60, RPMBurst: 60, RateLimitMode: "strict", LeaseSize: 10, LeaseTTL: time.Second, TPMLimit: 0, TPMBurst: 0, TPMReserveDefault: 512, CacheMode: "off", CacheTau: 0.95, CacheMaxEntries: 10000, EmbedderKind: "local"},
		},
		{
			name:    "tpm burst with zero limit rejected",
			env:     map[string]string{"PUSAQ_TPM_LIMIT": "0", "PUSAQ_TPM_BURST": "10"},
			wantErr: true,
		},
		{
			name:    "malformed tpm reserve rejected",
			env:     map[string]string{"PUSAQ_TPM_RESERVE_DEFAULT": "many"},
			wantErr: true,
		},
		{
			name:    "invalid cache mode rejected",
			env:     map[string]string{"PUSAQ_CACHE_MODE": "serve"},
			wantErr: true,
		},
		{
			name:    "tau above one rejected",
			env:     map[string]string{"PUSAQ_CACHE_TAU": "1.5"},
			wantErr: true,
		},
		{
			name:    "embedder naming unconfigured upstream rejected",
			env:     map[string]string{"PUSAQ_EMBEDDER": "openai:gemini"},
			wantErr: true,
		},
		{
			name: "openai embedder requires a model",
			env: map[string]string{
				"PUSAQ_UPSTREAMS": "gemini=http://g:1",
				"PUSAQ_EMBEDDER":  "openai:gemini",
			},
			wantErr: true,
		},
		{
			name:    "malformed api key pair rejected",
			env:     map[string]string{"PUSAQ_API_KEYS": "sk-abc"},
			wantErr: true,
		},
		{
			name:    "empty tenant rejected",
			env:     map[string]string{"PUSAQ_API_KEYS": "sk-abc:"},
			wantErr: true,
		},
		{
			name:    "duplicate api key rejected",
			env:     map[string]string{"PUSAQ_API_KEYS": "sk-abc:one,sk-abc:two"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(lookupFrom(tt.env))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			gotRoutes := got.Routes
			gotUpstreams := got.Upstreams
			got.Routes, got.Upstreams = nil, nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
			}
			if len(gotUpstreams) == 0 {
				t.Fatal("Upstreams empty")
			}
			if len(gotRoutes) != 3 {
				t.Fatalf("Routes has %d aliases, want 3", len(gotRoutes))
			}
			for _, alias := range []string{"pusaq/fast", "pusaq/best", "pusaq/auto"} {
				if _, ok := gotRoutes[alias]; !ok {
					t.Fatalf("Routes missing alias %q", alias)
				}
			}
		})
	}
}

func TestLoadAPIKeys(t *testing.T) {
	got, err := Load(lookupFrom(map[string]string{
		"PUSAQ_API_KEYS": " sk-a:team-a , sk-b:team-b ",
	}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := map[string]string{"sk-a": "team-a", "sk-b": "team-b"}
	if !reflect.DeepEqual(got.APIKeys, want) {
		t.Fatalf("APIKeys = %+v, want %+v", got.APIKeys, want)
	}
}

func TestLoadNoAPIKeysMeansDevMode(t *testing.T) {
	got, err := Load(lookupFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got.APIKeys) != 0 {
		t.Fatalf("APIKeys = %+v, want empty (dev mode)", got.APIKeys)
	}
}

func TestUpstreamKeysAttached(t *testing.T) {
	got, err := Load(lookupFrom(map[string]string{
		"PUSAQ_UPSTREAMS":           "gemini=http://g:1,mock=http://m:2",
		"PUSAQ_UPSTREAM_KEY_GEMINI": "sk-gem",
	}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.Upstreams[0].APIKey != "sk-gem" {
		t.Fatalf("gemini key = %q, want attached", got.Upstreams[0].APIKey)
	}
	if got.Upstreams[1].APIKey != "" {
		t.Fatal("mock upstream gained a key it was never given")
	}
}

func TestRouteModelOverrides(t *testing.T) {
	got, err := Load(lookupFrom(map[string]string{
		"PUSAQ_ROUTES": "pusaq/fast=gemini-2.5-flash, pusaq/best=gemini-2.5-pro",
	}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.Routes["pusaq/fast"].Model != "gemini-2.5-flash" {
		t.Fatalf("fast model = %q", got.Routes["pusaq/fast"].Model)
	}
	if got.Routes["pusaq/best"].Model != "gemini-2.5-pro" {
		t.Fatalf("best model = %q", got.Routes["pusaq/best"].Model)
	}
	if got.Routes["pusaq/auto"].Model != "mock-model" {
		t.Fatalf("auto model = %q, want untouched default", got.Routes["pusaq/auto"].Model)
	}
}

func TestRouteOverrideUnknownAliasRejected(t *testing.T) {
	if _, err := Load(lookupFrom(map[string]string{"PUSAQ_ROUTES": "pusaq/turbo=x"})); err == nil {
		t.Fatal("unknown alias accepted")
	}
	if _, err := Load(lookupFrom(map[string]string{"PUSAQ_ROUTES": "pusaq/fast"})); err == nil {
		t.Fatal("malformed pair accepted")
	}
}

func TestOpenAIEmbedderAccepted(t *testing.T) {
	got, err := Load(lookupFrom(map[string]string{
		"PUSAQ_UPSTREAMS":   "gemini=http://g:1",
		"PUSAQ_EMBEDDER":    "openai:gemini",
		"PUSAQ_EMBED_MODEL": "gemini-embedding-001",
	}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.EmbedderKind != "openai:gemini" || got.EmbedModel != "gemini-embedding-001" {
		t.Fatalf("embedder = %q model %q", got.EmbedderKind, got.EmbedModel)
	}
}
