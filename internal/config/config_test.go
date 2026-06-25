package config

import "testing"

func validConfig() Config {
	c := Config{Env: EnvDevelopment}
	c.HTTP.Port = 8080
	c.Database.URL = "postgres://forge:forge@localhost:5432/forge"
	c.Database.MaxConns = 10
	c.Database.MinConns = 2
	c.Telemetry.SampleRatio = 1
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
		{"sample ratio out of range", func(c *Config) { c.Telemetry.SampleRatio = 2 }},
		{"port out of range", func(c *Config) { c.HTTP.Port = 0 }},
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
