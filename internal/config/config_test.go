package config

import (
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
			want: Config{Addr: ":8080", DrainDelay: time.Second, ShutdownGrace: 10 * time.Second, TelemetryBuffer: 1024},
		},
		{
			name: "all overrides applied",
			env: map[string]string{
				"PUSAQ_ADDR":             ":9090",
				"PUSAQ_DRAIN_DELAY":      "0s",
				"PUSAQ_SHUTDOWN_GRACE":   "30s",
				"PUSAQ_TELEMETRY_BUFFER": "256",
			},
			want: Config{Addr: ":9090", DrainDelay: 0, ShutdownGrace: 30 * time.Second, TelemetryBuffer: 256},
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
			if got != tt.want {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
