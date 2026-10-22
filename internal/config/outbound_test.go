package config

import (
	"strings"
	"testing"
)

func TestOutboundURLConfiguration(t *testing.T) {
	for _, embedding := range []bool{false, true} {
		for _, raw := range []string{
			"https://example.test/v1", "http://mock:9090/v1", "ftp://example.test/v1", "https://private-url-sentinel@example.test/v1", "https://example.test/v1?private-url-sentinel", "https://example.test/v1?", "https://example.test/v1#private-url-sentinel", "https://example.test/v1#", "https://example.test:65536/v1", "https://example.test:/v1", "//example.test/v1",
		} {
			env := map[string]string{"PUSAQ_DEV_MODE": "true"}
			if embedding {
				env["PUSAQ_EMBEDDER"] = "openai:" + raw
				env["PUSAQ_EMBED_MODEL"] = "test-embedding"
			} else {
				env["PUSAQ_UPSTREAMS"] = "test=" + raw
			}
			_, err := Load(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
			valid := raw == "https://example.test/v1" || raw == "http://mock:9090/v1"
			if (err == nil) != valid {
				t.Fatalf("embedding=%v url=%s error=%v", embedding, raw, err)
			}
			if err != nil && strings.Contains(err.Error(), "private-url-sentinel") {
				t.Fatal("URL echoed in config error")
			}
		}
	}
}

func TestCredentialEndpointTransport(t *testing.T) {
	for _, embedding := range []bool{false, true} {
		for _, dev := range []bool{false, true} {
			for _, raw := range []string{
				"https://example.test/v1", "http://example.test/v1", "http://localhost:9090/v1", "http://127.0.0.1:9090/v1", "http://[::1]:9090/v1", "http://localhost.evil.test/v1", "http://127.0.0.1.nip.io/v1",
			} {
				env := map[string]string{"PUSAQ_API_KEYS": "test-client:tenant"}
				if dev {
					delete(env, "PUSAQ_API_KEYS")
					env["PUSAQ_DEV_MODE"] = "true"
				}
				if embedding {
					env["PUSAQ_EMBEDDER"] = "openai:" + raw
					env["PUSAQ_EMBED_MODEL"] = "test-embedding"
					env["PUSAQ_EMBED_KEY"] = "private-credential-sentinel"
				} else {
					env["PUSAQ_UPSTREAMS"] = "test=" + raw
					env["PUSAQ_UPSTREAM_KEY_TEST"] = "private-credential-sentinel"
				}
				_, err := Load(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
				valid := strings.HasPrefix(raw, "https://") || (dev && (raw == "http://localhost:9090/v1" || raw == "http://127.0.0.1:9090/v1" || raw == "http://[::1]:9090/v1"))
				if (err == nil) != valid {
					t.Fatalf("embedding=%v dev=%v url=%s error=%v", embedding, dev, raw, err)
				}
				if err != nil && strings.Contains(err.Error(), "private-credential-sentinel") {
					t.Fatal("credential echoed in config error")
				}
			}
		}
	}
}
