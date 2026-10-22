package config

import "testing"

func TestAuthenticationModeConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"missing", map[string]string{}, true},
		{"disabled", map[string]string{"PUSAQ_DEV_MODE": "false"}, true},
		{"development", map[string]string{"PUSAQ_DEV_MODE": "true"}, false},
		{"authenticated", map[string]string{"PUSAQ_API_KEYS": "test-key:tenant"}, false},
		{"explicit authenticated", map[string]string{"PUSAQ_DEV_MODE": "false", "PUSAQ_API_KEYS": "test-key:tenant"}, false},
		{"conflicting", map[string]string{"PUSAQ_DEV_MODE": "true", "PUSAQ_API_KEYS": "test-key:tenant"}, true},
		{"empty keys", map[string]string{"PUSAQ_DEV_MODE": "true", "PUSAQ_API_KEYS": ""}, true},
		{"invalid", map[string]string{"PUSAQ_DEV_MODE": "private-invalid-value"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok })
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
