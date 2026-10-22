package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func developmentLookupFrom(env map[string]string) func(string) (string, bool) {
	values := make(map[string]string, len(env)+1)
	for k, v := range env {
		values[k] = v
	}
	if _, ok := values["PUSAQ_API_KEYS"]; !ok {
		values["PUSAQ_DEV_MODE"] = "true"
	}
	return func(key string) (string, bool) {
		v, ok := values[key]
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
			name: "defaults in explicit development mode",
			env:  map[string]string{},
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024, UpstreamTimeout: 30 * time.Second, RPMLimit: 60, RPMBurst: 60, RateLimitMode: "strict", LeaseSize: 10, LeaseTTL: time.Second, TPMLimit: 100000, TPMBurst: 100000, TPMReserveDefault: 512, CacheMode: "off", CacheTau: 0.95, CacheMaxEntries: 10000, EmbedderKind: "local", RoutingRules: defaultRoutingRules(), RoutingDefault: "pusaq/fast", BreakerWindow: 20, BreakerMinSamples: 10, BreakerThreshold: 0.5, BreakerCooldown: 10 * time.Second, BreakerProbes: 3},
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
			want: Config{Addr: ":9091", DrainDelay: 0, ShutdownGrace: 30 * time.Second, TelemetryBuffer: 256, UpstreamTimeout: 5 * time.Second, RedisAddr: "redis:6379", RPMLimit: 120, RPMBurst: 30, RateLimitMode: "lease", LeaseSize: 5, LeaseTTL: 500 * time.Millisecond, TPMLimit: 6000, TPMBurst: 2000, TPMReserveDefault: 256, CacheMode: "shadow", CacheTau: 0.9, CacheMaxEntries: 500, EmbedderKind: "local", RoutingRules: defaultRoutingRules(), RoutingDefault: "pusaq/fast", BreakerWindow: 20, BreakerMinSamples: 10, BreakerThreshold: 0.5, BreakerCooldown: 10 * time.Second, BreakerProbes: 3},
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
			env:     map[string]string{"PUSAQ_RATELIMIT_MODE": "lease", "PUSAQ_RPM_BURST": "5", "PUSAQ_LEASE_SIZE": "6"},
			wantErr: true,
		},
		{
			name:    "non-positive lease ttl rejected",
			env:     map[string]string{"PUSAQ_LEASE_TTL": "0s"},
			wantErr: true,
		},
		{
			name: "price table parsed with its version",
			env:  map[string]string{"PUSAQ_PRICES": "mock-model=0.10/0.40, other=1/2", "PUSAQ_PRICE_TABLE_VERSION": "v1"},
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024, UpstreamTimeout: 30 * time.Second, RPMLimit: 60, RPMBurst: 60, RateLimitMode: "strict", LeaseSize: 10, LeaseTTL: time.Second, TPMLimit: 100000, TPMBurst: 100000, TPMReserveDefault: 512, CacheMode: "off", CacheTau: 0.95, CacheMaxEntries: 10000, EmbedderKind: "local", RoutingRules: defaultRoutingRules(), RoutingDefault: "pusaq/fast", BreakerWindow: 20, BreakerMinSamples: 10, BreakerThreshold: 0.5, BreakerCooldown: 10 * time.Second, BreakerProbes: 3, Prices: map[string]Price{"mock-model": {0.10, 0.40}, "other": {1, 2}}, PriceTableVersion: "v1"},
		},
		{
			name:    "price table requires a version",
			env:     map[string]string{"PUSAQ_PRICES": "mock-model=0.10/0.40"},
			wantErr: true,
		},
		{
			name:    "version without a price table rejected",
			env:     map[string]string{"PUSAQ_PRICE_TABLE_VERSION": "v1"},
			wantErr: true,
		},
		{
			name:    "price table must cover every route model",
			env:     map[string]string{"PUSAQ_ROUTES": "pusaq/best=other-model", "PUSAQ_PRICES": "mock-model=0.10/0.40", "PUSAQ_PRICE_TABLE_VERSION": "v1"},
			wantErr: true,
		},
		{
			name:    "price without output rate rejected",
			env:     map[string]string{"PUSAQ_PRICES": "mock-model=0.10", "PUSAQ_PRICE_TABLE_VERSION": "v1"},
			wantErr: true,
		},
		{
			name:    "negative price rejected",
			env:     map[string]string{"PUSAQ_PRICES": "mock-model=-1/0.40", "PUSAQ_PRICE_TABLE_VERSION": "v1"},
			wantErr: true,
		},
		{
			name:    "duplicate price rejected",
			env:     map[string]string{"PUSAQ_PRICES": "mock-model=1/1,mock-model=2/2", "PUSAQ_PRICE_TABLE_VERSION": "v1"},
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
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024, UpstreamTimeout: 30 * time.Second, RPMLimit: 60, RPMBurst: 60, RateLimitMode: "strict", LeaseSize: 10, LeaseTTL: time.Second, TPMLimit: 0, TPMBurst: 0, TPMReserveDefault: 512, CacheMode: "off", CacheTau: 0.95, CacheMaxEntries: 10000, EmbedderKind: "local", RoutingRules: defaultRoutingRules(), RoutingDefault: "pusaq/fast", BreakerWindow: 20, BreakerMinSamples: 10, BreakerThreshold: 0.5, BreakerCooldown: 10 * time.Second, BreakerProbes: 3},
		},
		{
			name: "empty tpm burst means default",
			env:  map[string]string{"PUSAQ_TPM_LIMIT": "6000", "PUSAQ_TPM_BURST": ""},
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024, UpstreamTimeout: 30 * time.Second, RPMLimit: 60, RPMBurst: 60, RateLimitMode: "strict", LeaseSize: 10, LeaseTTL: time.Second, BreakerWindow: 20, BreakerMinSamples: 10, BreakerThreshold: 0.5, BreakerCooldown: 10 * time.Second, BreakerProbes: 3, TPMLimit: 6000, TPMBurst: 6000, TPMReserveDefault: 512, CacheMode: "off", CacheTau: 0.95, CacheMaxEntries: 10000, EmbedderKind: "local", RoutingRules: defaultRoutingRules(), RoutingDefault: "pusaq/fast"},
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
			name:    "embedder without an absolute URL rejected",
			env:     map[string]string{"PUSAQ_EMBEDDER": "openai:gemini"},
			wantErr: true,
		},
		{
			name:    "openai embedder requires a model",
			env:     map[string]string{"PUSAQ_EMBEDDER": "openai:http://g:1/v1"},
			wantErr: true,
		},
		{
			name:    "embed key without an openai embedder rejected",
			env:     map[string]string{"PUSAQ_EMBED_KEY": "sk-e"},
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
			got, err := Load(developmentLookupFrom(tt.env))
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
			tt.want.DevMode = len(tt.want.APIKeys) == 0
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
			}
			if len(gotUpstreams) == 0 {
				t.Fatal("Upstreams empty")
			}
			if len(gotRoutes) != 2 {
				t.Fatalf("Routes has %d aliases, want 2", len(gotRoutes))
			}
			for _, alias := range []string{"pusaq/fast", "pusaq/best"} {
				if _, ok := gotRoutes[alias]; !ok {
					t.Fatalf("Routes missing alias %q", alias)
				}
			}
		})
	}
}

func TestLoadAPIKeys(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{
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

func TestLoadExplicitDevelopmentMode(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got.APIKeys) != 0 {
		t.Fatalf("APIKeys = %+v, want empty (dev mode)", got.APIKeys)
	}
}

func TestUpstreamKeysAttached(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{
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
	got, err := Load(developmentLookupFrom(map[string]string{
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
	if _, ok := got.Routes["pusaq/auto"]; ok {
		t.Fatal("pusaq/auto must not be a route")
	}
}

func TestRouteOverrideAutoRejected(t *testing.T) {
	if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_ROUTES": "pusaq/auto=x"})); err == nil {
		t.Fatal("pusaq/auto accepted as a route")
	}
}

func TestRoutingRulesReplaceDefaults(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_ROUTING_RULES": "toll=pusaq/best, default=pusaq/fast"}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !reflect.DeepEqual(got.RoutingRules, map[string]string{"toll": "pusaq/best"}) || got.RoutingDefault != "pusaq/fast" {
		t.Fatalf("rules = %v default = %q, want the configured table only", got.RoutingRules, got.RoutingDefault)
	}
}

func TestRoutingRulesRejections(t *testing.T) {
	for name, v := range map[string]string{
		"missing default":   "classify=pusaq/fast",
		"auto target":       "classify=pusaq/auto,default=pusaq/fast",
		"unknown alias":     "classify=pusaq/turbo,default=pusaq/fast",
		"duplicate tag":     "classify=pusaq/fast,classify=pusaq/best,default=pusaq/fast",
		"duplicate default": "default=pusaq/fast,default=pusaq/best",
		"malformed pair":    "classify,default=pusaq/fast",
		"tag charset":       "deep reason=pusaq/best,default=pusaq/fast",
	} {
		if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_ROUTING_RULES": v})); err == nil {
			t.Fatalf("%s accepted: %q", name, v)
		}
	}
}

func TestRouteOverrideUnknownAliasRejected(t *testing.T) {
	if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_ROUTES": "pusaq/turbo=x"})); err == nil {
		t.Fatal("unknown alias accepted")
	}
	if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_ROUTES": "pusaq/fast"})); err == nil {
		t.Fatal("malformed pair accepted")
	}
}

func TestOpenAIEmbedderAccepted(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{
		"PUSAQ_EMBEDDER":    "openai:https://g.example/v1beta/openai/",
		"PUSAQ_EMBED_MODEL": "gemini-embedding-001",
		"PUSAQ_EMBED_KEY":   "sk-e",
	}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.EmbedderKind != "openai" || got.EmbedURL != "https://g.example/v1beta/openai" ||
		got.EmbedModel != "gemini-embedding-001" || got.EmbedKey != "sk-e" {
		t.Fatalf("embedder = %q %q model %q key %q", got.EmbedderKind, got.EmbedURL, got.EmbedModel, got.EmbedKey)
	}
	if len(got.Upstreams) != 1 || got.Upstreams[0].Name != "mock" {
		t.Fatalf("upstreams = %+v, want the chat chain untouched by the embedder", got.Upstreams)
	}
}

func TestBreakerConfigOverrides(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{
		"PUSAQ_BREAKER_WINDOW":      "8",
		"PUSAQ_BREAKER_MIN_SAMPLES": "4",
		"PUSAQ_BREAKER_THRESHOLD":   "0.25",
		"PUSAQ_BREAKER_COOLDOWN":    "2s",
		"PUSAQ_BREAKER_PROBES":      "1",
	}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.BreakerWindow != 8 || got.BreakerMinSamples != 4 || got.BreakerThreshold != 0.25 || got.BreakerCooldown != 2*time.Second || got.BreakerProbes != 1 {
		t.Fatalf("breaker config = %+v", got)
	}
}

func TestBreakerConfigRejections(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"zero window":           {"PUSAQ_BREAKER_WINDOW": "0"},
		"min above window":      {"PUSAQ_BREAKER_WINDOW": "5", "PUSAQ_BREAKER_MIN_SAMPLES": "6"},
		"threshold one":         {"PUSAQ_BREAKER_THRESHOLD": "1"},
		"threshold zero":        {"PUSAQ_BREAKER_THRESHOLD": "0"},
		"non-positive cooldown": {"PUSAQ_BREAKER_COOLDOWN": "0s"},
		"zero probes":           {"PUSAQ_BREAKER_PROBES": "0"},
	} {
		if _, err := Load(developmentLookupFrom(env)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestSmallRequestBurstLoadsInStrictMode(t *testing.T) {
	cfg, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_RPM_LIMIT": "5"}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.RPMBurst != 5 || cfg.RateLimitMode != RateLimitStrict {
		t.Fatalf("cfg = burst %g mode %s", cfg.RPMBurst, cfg.RateLimitMode)
	}
}

func TestTokenReserveMustFitTheBurst(t *testing.T) {
	if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_TPM_BURST": "500"})); err == nil {
		t.Fatal("a default reservation of 512 against a burst of 500 accepted")
	}
	if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_TPM_BURST": "512"})); err != nil {
		t.Fatalf("reservation equal to the burst rejected: %v", err)
	}
	if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_TPM_LIMIT": "0"})); err != nil {
		t.Fatalf("disabled token limiting rejected: %v", err)
	}
}

func TestNonFiniteNumbersRejected(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"rpm limit NaN":      {"PUSAQ_RPM_LIMIT": "NaN"},
		"rpm limit Inf":      {"PUSAQ_RPM_LIMIT": "Inf"},
		"rpm burst NaN":      {"PUSAQ_RPM_BURST": "NaN"},
		"breaker threshold":  {"PUSAQ_BREAKER_THRESHOLD": "NaN"},
		"tpm limit NaN":      {"PUSAQ_TPM_LIMIT": "NaN"},
		"tpm burst infinity": {"PUSAQ_TPM_BURST": "infinity"},
		"cache tau":          {"PUSAQ_CACHE_TAU": "NaN"},
		"price":              {"PUSAQ_PRICES": "mock-model=NaN/0.40", "PUSAQ_PRICE_TABLE_VERSION": "v1"},
		"budget":             policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:budget_usd=NaN"}),
		"budget infinity":    policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:budget_usd=Inf"}),
	} {
		if _, err := Load(developmentLookupFrom(env)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func policyEnv(extra map[string]string) map[string]string {
	env := map[string]string{
		"PUSAQ_API_KEYS":            "k-a:tenant-a,k-b:tenant-b",
		"PUSAQ_REDIS_ADDR":          "redis:6379",
		"PUSAQ_PRICES":              "mock-model=0.10/0.40",
		"PUSAQ_PRICE_TABLE_VERSION": "v1",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func TestTenantPoliciesParsed(t *testing.T) {
	got, err := Load(developmentLookupFrom(policyEnv(map[string]string{
		"PUSAQ_TENANT_POLICIES": "tenant-a:models=pusaq/fast|pusaq/best, max_prompt_chars=8000, budget_usd=50, fail_closed=true, sensitive=block, injection=log, cache=off; tenant-b:budget_usd=10, cache=on",
	})))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := map[string]TenantPolicy{
		"tenant-a": {Models: []string{"pusaq/fast", "pusaq/best"}, MaxPromptChars: 8000, BudgetUSD: 50, FailClosed: true, Sensitive: "block", Injection: "log", CacheOff: true},
		"tenant-b": {BudgetUSD: 10},
	}
	if !reflect.DeepEqual(got.TenantPolicies, want) {
		t.Fatalf("policies = %+v, want %+v", got.TenantPolicies, want)
	}
	if !got.HasBudgets() {
		t.Fatal("HasBudgets() = false")
	}
}

func TestTenantPoliciesDevTenantWithoutKeys(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_TENANT_POLICIES": "dev:models=pusaq/fast,max_prompt_chars=10"}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.TenantPolicies["dev"].MaxPromptChars != 10 || got.HasBudgets() {
		t.Fatalf("policies = %+v", got.TenantPolicies)
	}
}

func TestTenantPoliciesRejections(t *testing.T) {
	cases := map[string]map[string]string{
		"unknown tenant":             policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-z:budget_usd=1"}),
		"dev tenant when keys exist": policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "dev:budget_usd=1"}),
		"no colon":                   policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a"}),
		"empty fields":               policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:"}),
		"duplicate tenant":           policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:budget_usd=1;tenant-a:budget_usd=2"}),
		"duplicate field":            policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:budget_usd=1,budget_usd=2"}),
		"unknown field":              policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:colour=blue"}),
		"auto in models":             policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:models=pusaq/auto"}),
		"unknown alias":              policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:models=pusaq/turbo"}),
		"empty alias":                policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:models=pusaq/fast|"}),
		"duplicate alias":            policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:models=pusaq/fast|pusaq/fast"}),
		"zero prompt cap":            policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:max_prompt_chars=0"}),
		"non-integer prompt cap":     policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:max_prompt_chars=big"}),
		"negative budget":            policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:budget_usd=-1"}),
		"bad bool":                   policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:fail_closed=yes"}),
		"bad sensitive action":       policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:sensitive=deny"}),
		"bad injection action":       policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:injection=true"}),
		"bad cache value":            policyEnv(map[string]string{"PUSAQ_TENANT_POLICIES": "tenant-a:cache=shadow"}),
		"budget without prices": {
			"PUSAQ_API_KEYS": "k-a:tenant-a", "PUSAQ_REDIS_ADDR": "redis:6379",
			"PUSAQ_TENANT_POLICIES": "tenant-a:budget_usd=1",
		},
		"budget without redis": {
			"PUSAQ_API_KEYS": "k-a:tenant-a", "PUSAQ_PRICES": "mock-model=1/1", "PUSAQ_PRICE_TABLE_VERSION": "v1",
			"PUSAQ_TENANT_POLICIES": "tenant-a:budget_usd=1",
		},
		"fail_closed without redis": {
			"PUSAQ_API_KEYS":        "k-a:tenant-a",
			"PUSAQ_TENANT_POLICIES": "tenant-a:fail_closed=true",
		},
	}
	for name, env := range cases {
		if _, err := Load(developmentLookupFrom(env)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestRegionAndAdminTokenLoaded(t *testing.T) {
	got, err := Load(developmentLookupFrom(map[string]string{
		"PUSAQ_REGION":      " us-east1 ",
		"PUSAQ_ADMIN_TOKEN": "0123456789abcdef",
	}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.Region != "us-east1" || got.AdminToken != "0123456789abcdef" {
		t.Fatalf("Region/AdminToken = %q/%q", got.Region, got.AdminToken)
	}
	plain, err := Load(developmentLookupFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if plain.Region != "" || plain.AdminToken != "" {
		t.Fatalf("defaults = %q/%q, want empty (unlabeled, no admin endpoints)", plain.Region, plain.AdminToken)
	}
}

func TestRegionAndAdminTokenRejections(t *testing.T) {
	for _, env := range []map[string]string{
		{"PUSAQ_REGION": "us east1"},
		{"PUSAQ_REGION": "us/east1"},
		{"PUSAQ_REGION": "   "},
		{"PUSAQ_ADMIN_TOKEN": "   "},
		{"PUSAQ_ADMIN_TOKEN": "short"},
		{"PUSAQ_ADMIN_TOKEN": "has whitespace in it"},
	} {
		if _, err := Load(developmentLookupFrom(env)); err == nil {
			t.Fatalf("env %v accepted, want an error", env)
		}
	}
}

func TestConfigErrorsNeverEchoSecrets(t *testing.T) {
	const secret = "sk-live-TOPSECRET123"
	for name, env := range map[string]map[string]string{
		"duplicate key":      {"PUSAQ_API_KEYS": secret + ":acme," + secret + ":beta"},
		"wrong separator":    {"PUSAQ_API_KEYS": secret + "=acme"},
		"url with password":  {"PUSAQ_UPSTREAMS": "a=https://user:" + secret + "@/nohost"},
		"malformed url pair": {"PUSAQ_UPSTREAMS": "https://user:" + secret + "@host"},
		"embedder url":       {"PUSAQ_EMBEDDER": "openai:https://user:" + secret + "@/nohost"},
		"embedder no model":  {"PUSAQ_EMBEDDER": "openai:https://user:" + secret + "@host/v1"},
	} {
		_, err := Load(developmentLookupFrom(env))
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s: error echoes the secret: %v", name, err)
		}
	}
}

func TestEmptyAPIKeysFailsStartup(t *testing.T) {
	if _, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_API_KEYS": ""})); err == nil {
		t.Fatal("empty PUSAQ_API_KEYS accepted as dev mode")
	}
}

func TestUpstreamKeyVariableCollisionRejected(t *testing.T) {
	_, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_UPSTREAMS": "my-api=http://a:1,my_api=http://b:1"}))
	if err == nil {
		t.Fatal("upstreams sharing PUSAQ_UPSTREAM_KEY_MY_API accepted")
	}
	if !strings.Contains(err.Error(), "PUSAQ_UPSTREAM_KEY_MY_API") {
		t.Fatalf("error = %v, want the shared variable named", err)
	}
}

func TestAPIKeyFieldsAreTrimmed(t *testing.T) {
	cfg, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_API_KEYS": "sk-a : acme"}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.APIKeys["sk-a"] != "acme" {
		t.Fatalf("keys = %q, want sk-a → acme", cfg.APIKeys)
	}
}

func TestUpstreamURLsAreVersionedBases(t *testing.T) {
	def, err := Load(developmentLookupFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if def.Upstreams[0].URL != "http://localhost:9090/v1" {
		t.Fatalf("default upstream = %q, want the mock's /v1 base", def.Upstreams[0].URL)
	}
	got, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_UPSTREAMS": "gemini=https://g.example/v1beta/openai/"}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.Upstreams[0].URL != "https://g.example/v1beta/openai" {
		t.Fatalf("upstream = %q, want the base without its trailing slash", got.Upstreams[0].URL)
	}
}
