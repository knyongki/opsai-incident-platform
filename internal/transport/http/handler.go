// Package httpapi provides the HTTP boundary of the application.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
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

// NewHandler supports exact paths, including JSON errors for unknown routes.
func NewHandler(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var status string
		switch r.URL.Path {
		case "/health":
			status = "ok"
		case "/ready":
			// No external dependencies exist in Milestone 1.
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
