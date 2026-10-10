// Package httpapi provides the HTTP boundary of the application.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type successResponse struct {
	Data any `json:"data"`
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type statusData struct {
	Status string `json:"status"`
}

// ReadinessChecker is the small consumer-owned interface needed by /ready.
// Both pgxpool.Pool and test doubles can satisfy it without an adapter.
type ReadinessChecker interface {
	Ping(context.Context) error
}

const readinessTimeout = 2 * time.Second

// NewHandler supports exact paths, including JSON errors for unknown routes.
func NewHandler(logger *slog.Logger, database ReadinessChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var status string
		switch r.URL.Path {
		case "/health":
			status = "ok"
		case "/ready":
			status = "ready"
		default:
			writeJSON(logger, w, http.StatusNotFound, errorResponse{apiError{
				Code: "NOT_FOUND", Message: "The requested resource was not found.",
			}})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(logger, w, http.StatusMethodNotAllowed, errorResponse{apiError{
				Code: "METHOD_NOT_ALLOWED", Message: "The request method is not allowed.",
			}})
			return
		}
		if r.URL.Path == "/ready" {
			ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
			defer cancel()
			if database == nil || database.Ping(ctx) != nil {
				// Keep driver errors and connection details out of logs and responses.
				logger.Warn("PostgreSQL readiness check failed", "error_code", "DATABASE_UNAVAILABLE")
				if r.Method == http.MethodHead {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				writeJSON(logger, w, http.StatusServiceUnavailable, errorResponse{apiError{
					Code: "DATABASE_UNAVAILABLE", Message: "The database is unavailable.",
				}})
				return
			}
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(logger, w, http.StatusOK, successResponse{Data: statusData{Status: status}})
	})
}

func writeJSON(logger *slog.Logger, w http.ResponseWriter, status int, body any) {
	// Encode before sending headers so an encoding failure can still return 500.
	data, err := json.Marshal(body)
	if err != nil {
		logger.Error("encode HTTP response", "error", err)
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"INTERNAL_ERROR","message":"An internal error occurred."}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(data, '\n')); err != nil {
		logger.Error("write HTTP response", "error", err)
	}
}
