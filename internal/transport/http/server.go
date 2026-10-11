package httpapi

import (
	"log/slog"
	"net/http"
	"time"
)

func NewServer(address string, logger *slog.Logger, database ReadinessChecker, devices ...DeviceService) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           NewHandler(logger, database, devices...),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
