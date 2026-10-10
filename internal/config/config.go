// Package config loads and validates process configuration.
package config

import (
	"errors"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppEnv      string
	HTTPAddress string
	LogLevel    slog.Level
	DatabaseURL string
}

func Load() (Config, error) {
	cfg := Config{
		AppEnv:      value("APP_ENV", "development"),
		HTTPAddress: value("HTTP_ADDRESS", "127.0.0.1:8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	databaseURL, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") ||
		databaseURL.Hostname() == "" || len(databaseURL.Path) <= 1 || databaseURL.Fragment != "" {
		// Parser errors may contain the connection string, including credentials.
		return Config{}, errors.New("DATABASE_URL must be a PostgreSQL URL with a host and database name")
	}
	if strings.TrimSpace(cfg.AppEnv) == "" {
		return Config{}, errors.New("APP_ENV must not be whitespace")
	}
	host, port, err := net.SplitHostPort(cfg.HTTPAddress)
	if err != nil || strings.TrimSpace(host) == "" {
		return Config{}, errors.New("HTTP_ADDRESS must contain an explicit host and numeric port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, errors.New("HTTP_ADDRESS port must be between 1 and 65535")
	}
	switch strings.ToUpper(value("LOG_LEVEL", "INFO")) {
	case "DEBUG":
		cfg.LogLevel = slog.LevelDebug
	case "INFO":
		cfg.LogLevel = slog.LevelInfo
	case "WARN":
		cfg.LogLevel = slog.LevelWarn
	case "ERROR":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, errors.New("LOG_LEVEL must be DEBUG, INFO, WARN, or ERROR")
	}
	return cfg, nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
