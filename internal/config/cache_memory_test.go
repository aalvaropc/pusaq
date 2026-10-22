package config

import "testing"

func TestCacheByteBudgets(t *testing.T) {
	cfg, err := Load(developmentLookupFrom(map[string]string{"PUSAQ_CACHE_MAX_BYTES": "1024", "PUSAQ_CACHE_QUEUE_MAX_BYTES": "512"}))
	if err != nil || cfg.CacheMaxBytes != 1024 || cfg.CacheQueueMaxBytes != 512 {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	for _, key := range []string{"PUSAQ_CACHE_MAX_BYTES", "PUSAQ_CACHE_QUEUE_MAX_BYTES"} {
		for _, v := range []string{"0", "-1", "invalid", "9223372036854775808"} {
			if _, err := Load(developmentLookupFrom(map[string]string{key: v})); err == nil {
				t.Fatalf("accepted invalid %s", key)
			}
		}
	}
}
