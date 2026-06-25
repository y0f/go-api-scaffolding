// Package config loads and validates runtime configuration from the
// environment. All settings are twelve-factor: every field has an env var, a
// sensible default where one exists, and validation that fails fast at boot.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

type Config struct {
	Env       string          `env:"FORGE_ENV" envDefault:"development"`
	HTTP      HTTPConfig      `envPrefix:"FORGE_HTTP_"`
	Admin     AdminConfig     `envPrefix:"FORGE_ADMIN_"`
	Database  DatabaseConfig  `envPrefix:"FORGE_DB_"`
	Telemetry TelemetryConfig `envPrefix:"FORGE_OTEL_"`
	Auth      AuthConfig      `envPrefix:"FORGE_AUTH_"`
	Log       LogConfig       `envPrefix:"FORGE_LOG_"`
}

type HTTPConfig struct {
	Host               string        `env:"HOST" envDefault:"0.0.0.0"`
	Port               int           `env:"PORT" envDefault:"8080"`
	ReadHeaderTimeout  time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout        time.Duration `env:"READ_TIMEOUT" envDefault:"15s"`
	WriteTimeout       time.Duration `env:"WRITE_TIMEOUT" envDefault:"15s"`
	IdleTimeout        time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout    time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
	DrainDelay         time.Duration `env:"DRAIN_DELAY" envDefault:"2s"`
	CORSAllowedOrigins []string      `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`
	RateLimitPerSecond float64       `env:"RATE_LIMIT_PER_SECOND" envDefault:"50"`
	RateLimitBurst     int           `env:"RATE_LIMIT_BURST" envDefault:"100"`
	MaxBodyBytes       int64         `env:"MAX_BODY_BYTES" envDefault:"1048576"`
}

// AdminConfig controls the separate introspection listener that exposes
// pprof and expvar. It is never mounted on the public server.
type AdminConfig struct {
	Enabled bool   `env:"ENABLED" envDefault:"false"`
	Host    string `env:"HOST" envDefault:"127.0.0.1"`
	Port    int    `env:"PORT" envDefault:"6060"`
	Token   string `env:"TOKEN"`
}

type DatabaseConfig struct {
	URL               string        `env:"URL" envDefault:"postgres://forge:forge@localhost:5432/forge?sslmode=disable"`
	MaxConns          int32         `env:"MAX_CONNS" envDefault:"10"`
	MinConns          int32         `env:"MIN_CONNS" envDefault:"2"`
	MaxConnLifetime   time.Duration `env:"MAX_CONN_LIFETIME" envDefault:"1h"`
	MaxConnIdleTime   time.Duration `env:"MAX_CONN_IDLE_TIME" envDefault:"30m"`
	HealthCheckPeriod time.Duration `env:"HEALTH_CHECK_PERIOD" envDefault:"1m"`
	ConnectTimeout    time.Duration `env:"CONNECT_TIMEOUT" envDefault:"10s"`
}

type TelemetryConfig struct {
	ServiceName  string  `env:"SERVICE_NAME" envDefault:"forge"`
	OTLPEndpoint string  `env:"OTLP_ENDPOINT"`
	SampleRatio  float64 `env:"SAMPLE_RATIO" envDefault:"1.0"`
}

// AuthConfig selects how bearer tokens are verified. Exactly one of JWKSURL or
// PublicKeyPath is expected in production; development falls back to an
// ephemeral in-memory key (see internal/auth).
type AuthConfig struct {
	JWKSURL       string        `env:"JWKS_URL"`
	PublicKeyPath string        `env:"PUBLIC_KEY_PATH"`
	Issuer        string        `env:"ISSUER" envDefault:"forge"`
	Audience      string        `env:"AUDIENCE" envDefault:"forge"`
	DevTokenTTL   time.Duration `env:"DEV_TOKEN_TTL" envDefault:"24h"`
}

type LogConfig struct {
	Level  string `env:"LEVEL" envDefault:"info"`
	Format string `env:"FORMAT" envDefault:"json"`
}

// Load reads the environment into a Config and validates it.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) IsProduction() bool { return c.Env == EnvProduction }

func (c Config) ServiceVersion() string { return version }

// version is overridden at build time via -ldflags -X.
var version = "dev"

func (c Config) Validate() error {
	var errs []error
	if c.Env != EnvDevelopment && c.Env != EnvProduction {
		errs = append(errs, fmt.Errorf("FORGE_ENV must be %q or %q, got %q", EnvDevelopment, EnvProduction, c.Env))
	}
	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Errorf("FORGE_HTTP_PORT out of range: %d", c.HTTP.Port))
	}
	if c.Database.URL == "" {
		errs = append(errs, errors.New("FORGE_DB_URL is required"))
	}
	if c.Database.MinConns > c.Database.MaxConns {
		errs = append(errs, fmt.Errorf("FORGE_DB_MIN_CONNS (%d) exceeds MAX_CONNS (%d)", c.Database.MinConns, c.Database.MaxConns))
	}
	if c.IsProduction() && c.Auth.JWKSURL == "" && c.Auth.PublicKeyPath == "" {
		errs = append(errs, errors.New("production requires FORGE_AUTH_JWKS_URL or FORGE_AUTH_PUBLIC_KEY_PATH"))
	}
	if c.Admin.Enabled && c.Admin.Token == "" {
		errs = append(errs, errors.New("FORGE_ADMIN_TOKEN is required when FORGE_ADMIN_ENABLED is true"))
	}
	if c.Telemetry.SampleRatio < 0 || c.Telemetry.SampleRatio > 1 {
		errs = append(errs, fmt.Errorf("FORGE_OTEL_SAMPLE_RATIO must be in [0,1], got %v", c.Telemetry.SampleRatio))
	}
	if c.HTTP.RateLimitPerSecond <= 0 {
		errs = append(errs, fmt.Errorf("FORGE_HTTP_RATE_LIMIT_PER_SECOND must be > 0, got %v", c.HTTP.RateLimitPerSecond))
	}
	if c.HTTP.RateLimitBurst < 1 {
		errs = append(errs, fmt.Errorf("FORGE_HTTP_RATE_LIMIT_BURST must be >= 1, got %d", c.HTTP.RateLimitBurst))
	}
	if c.HTTP.MaxBodyBytes < 1 {
		errs = append(errs, fmt.Errorf("FORGE_HTTP_MAX_BODY_BYTES must be >= 1, got %d", c.HTTP.MaxBodyBytes))
	}
	return errors.Join(errs...)
}
