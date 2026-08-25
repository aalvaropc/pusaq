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
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024, MockURL: "http://localhost:9090", UpstreamTimeout: 30 * time.Second, RPMLimit: 60, RPMBurst: 60, TPMLimit: 100000, TPMBurst: 100000, TPMReserveDefault: 512},
		},
		{
			name: "all overrides applied",
			env: map[string]string{
				"PUSAQ_ADDR":                ":9091",
				"PUSAQ_DRAIN_DELAY":         "0s",
				"PUSAQ_SHUTDOWN_GRACE":      "30s",
				"PUSAQ_TELEMETRY_BUFFER":    "256",
				"PUSAQ_MOCK_URL":            "http://mock:9090/",
				"PUSAQ_UPSTREAM_TIMEOUT":    "5s",
				"PUSAQ_REDIS_ADDR":          "redis:6379",
				"PUSAQ_RPM_LIMIT":           "120",
				"PUSAQ_RPM_BURST":           "30",
				"PUSAQ_TPM_LIMIT":           "6000",
				"PUSAQ_TPM_BURST":           "2000",
				"PUSAQ_TPM_RESERVE_DEFAULT": "256",
			},
			want: Config{Addr: ":9091", DrainDelay: 0, ShutdownGrace: 30 * time.Second, TelemetryBuffer: 256, MockURL: "http://mock:9090", UpstreamTimeout: 5 * time.Second, RedisAddr: "redis:6379", RPMLimit: 120, RPMBurst: 30, TPMLimit: 6000, TPMBurst: 2000, TPMReserveDefault: 256},
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
			name:    "relative mock URL rejected",
			env:     map[string]string{"PUSAQ_MOCK_URL": "localhost:9090"},
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
			name:    "non-positive tpm limit rejected",
			env:     map[string]string{"PUSAQ_TPM_LIMIT": "-5"},
			wantErr: true,
		},
		{
			name:    "malformed tpm reserve rejected",
			env:     map[string]string{"PUSAQ_TPM_RESERVE_DEFAULT": "many"},
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
			got.Routes = nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
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
