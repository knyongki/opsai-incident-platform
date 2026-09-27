// Package logging constructs the application's structured logger.
package logging

import (
	"io"
	"log/slog"
)

func New(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}
