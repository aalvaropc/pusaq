package config

import (
	"testing"
	"time"
)

func TestRequestIOTimeouts(t *testing.T) {
	cfg, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_BODY_READ_TIMEOUT": "2s", "PUSAQ_WRITE_IDLE_TIMEOUT": "3s"}))
	if err != nil || cfg.BodyReadTimeout != 2*time.Second || cfg.WriteIdleTimeout != 3*time.Second {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	for _, key := range []string{"PUSAQ_BODY_READ_TIMEOUT", "PUSAQ_WRITE_IDLE_TIMEOUT"} {
		for _, v := range []string{"0s", "-1s", "invalid"} {
			if _, err := Load(developmentLookupFrom(map[string]string{key: v})); err == nil {
				t.Fatalf("accepted %s=%s", key, v)
			}
		}
	}
}
