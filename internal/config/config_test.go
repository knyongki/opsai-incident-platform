package config

import (
	"log/slog"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://opsai:local-example-only@127.0.0.1:5432/opsai?sslmode=disable")
	for _, tt := range []struct {
		name, env, address, level string
		invalid                   bool
	}{
		{name: "defaults"},
		{name: "overrides", env: "test", address: "localhost:9090", level: "debug"},
		{name: "IPv6", address: "[::1]:8080", level: "WARN"},
		{name: "error level", level: "ERROR"},
		{name: "bad address", address: "localhost", invalid: true},
		{name: "empty host", address: ":8080", invalid: true},
		{name: "bad port", address: "localhost:abc", invalid: true},
		{name: "zero port", address: "localhost:0", invalid: true},
		{name: "port range", address: "localhost:65536", invalid: true},
		{name: "bad level", level: "verbose", invalid: true},
		{name: "blank environment", env: "  ", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tt.env)
			t.Setenv("HTTP_ADDRESS", tt.address)
			t.Setenv("LOG_LEVEL", tt.level)
			cfg, err := Load()
			if (err != nil) != tt.invalid {
				t.Fatalf("Load() error = %v, want invalid %v", err, tt.invalid)
			}
			if tt.name == "defaults" && (cfg.AppEnv != "development" || cfg.HTTPAddress != "127.0.0.1:8080" || cfg.LogLevel != slog.LevelInfo) {
				t.Fatalf("unexpected defaults: %+v", cfg)
			}
			if tt.name == "overrides" && (cfg.AppEnv != "test" || cfg.HTTPAddress != "localhost:9090" || cfg.LogLevel != slog.LevelDebug) {
				t.Fatalf("overrides not applied: %+v", cfg)
			}
		})
	}
}

func TestDatabaseURL(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_ADDRESS", "")
	t.Setenv("LOG_LEVEL", "")
	for _, tt := range []struct {
		name, value string
		valid       bool
	}{
		{"missing", "", false},
		{"whitespace", "  ", false},
		{"wrong scheme", "https://localhost/opsai", false},
		{"no host", "postgres:///opsai", false},
		{"no database", "postgres://localhost", false},
		{"malformed", "postgres://user:example%zz@localhost/opsai", false},
		{"postgres", "postgres://opsai:example-only@localhost:5432/opsai?sslmode=disable", true},
		{"postgresql", "postgresql://localhost/opsai", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tt.value)
			cfg, err := Load()
			if (err == nil) != tt.valid {
				t.Fatalf("unexpected validity: error = %v", err)
			}
			if tt.valid && cfg.DatabaseURL != tt.value {
				t.Error("DATABASE_URL was not preserved")
			}
			if err != nil && strings.Contains(err.Error(), "example") {
				t.Error("configuration error exposed connection details")
			}
		})
	}
}
