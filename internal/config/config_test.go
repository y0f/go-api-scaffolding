package config

import "testing"

func validConfig() Config {
	c := Config{Env: EnvDevelopment}
	c.HTTP.Port = 8080
	c.HTTP.RateLimitPerSecond = 50
	c.HTTP.RateLimitBurst = 100
	c.HTTP.MaxBodyBytes = 1 << 20
	c.Database.URL = "postgres://forge:forge@localhost:5432/forge"
	c.Database.MaxConns = 10
	c.Database.MinConns = 2
	//forge:begin otlp
	c.Telemetry.SampleRatio = 1
	//forge:end otlp
	return c
}

func TestValidate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"unknown env", func(c *Config) { c.Env = "staging" }},
		{"missing database url", func(c *Config) { c.Database.URL = "" }},
		{"min exceeds max conns", func(c *Config) { c.Database.MinConns = 99 }},
		{"production needs verifier", func(c *Config) { c.Env = EnvProduction }},
		//forge:begin otlp
		{"sample ratio out of range", func(c *Config) { c.Telemetry.SampleRatio = 2 }},
		//forge:end otlp
		{"port out of range", func(c *Config) { c.HTTP.Port = 0 }},
		{"zero rate limit", func(c *Config) { c.HTTP.RateLimitPerSecond = 0 }},
		{"zero burst", func(c *Config) { c.HTTP.RateLimitBurst = 0 }},
		{"zero max body", func(c *Config) { c.HTTP.MaxBodyBytes = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestLoadParsesTrustedProxies(t *testing.T) {
	t.Setenv("FORGE_HTTP_TRUSTED_PROXIES", "10.0.0.0/8,fd00::/8")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.HTTP.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %v, want two prefixes", cfg.HTTP.TrustedProxies)
	}
	if got := cfg.HTTP.TrustedProxies[0].String(); got != "10.0.0.0/8" {
		t.Errorf("first prefix = %s, want 10.0.0.0/8", got)
	}
	if got := cfg.HTTP.TrustedProxies[1].String(); got != "fd00::/8" {
		t.Errorf("second prefix = %s, want fd00::/8", got)
	}
}

func TestLoadRejectsTrustedProxyWithoutPrefixLength(t *testing.T) {
	t.Setenv("FORGE_HTTP_TRUSTED_PROXIES", "10.0.0.1")
	if _, err := Load(); err == nil {
		t.Fatal("expected a parse error for a bare address without a prefix length")
	}
}

func TestLoadDefaultsToNoTrustedProxies(t *testing.T) {
	t.Setenv("FORGE_HTTP_TRUSTED_PROXIES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.HTTP.TrustedProxies) != 0 {
		t.Errorf("trusted proxies = %v, want none", cfg.HTTP.TrustedProxies)
	}
}
