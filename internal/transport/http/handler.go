// Package httpapi provides the HTTP boundary of the application.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path"
	"strings"
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

// Dependencies are the services the router exposes. A nil service leaves its
// routes unregistered, so tests can build a handler for one resource at a time.
type Dependencies struct {
	Database  ReadinessChecker
	Devices   DeviceService
	Incidents IncidentService
}

const readinessTimeout = 2 * time.Second

var (
	errNotFound         = errorResponse{apiError{Code: "NOT_FOUND", Message: "The requested resource was not found."}}
	errMethodNotAllowed = errorResponse{apiError{Code: "METHOD_NOT_ALLOWED", Message: "The request method is not allowed."}}
	errInternal         = errorResponse{apiError{Code: "INTERNAL_ERROR", Message: "An internal error occurred."}}
)

// NewHandler routes exact paths and keeps JSON envelopes for every outcome,
// including unknown paths and unsupported methods.
func NewHandler(logger *slog.Logger, deps Dependencies) http.Handler {
	rt := router{http.NewServeMux(), logger}
	rt.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(logger, w, http.StatusNotFound, errNotFound)
	})
	rt.route("/health", "GET, HEAD", map[string]http.HandlerFunc{
		http.MethodGet: func(w http.ResponseWriter, r *http.Request) { writeStatus(logger, w, r, "ok") },
	})
	rt.route("/ready", "GET, HEAD", map[string]http.HandlerFunc{
		http.MethodGet: func(w http.ResponseWriter, r *http.Request) { serveReady(logger, deps.Database, w, r) },
	})
	if deps.Devices != nil {
		deviceRoutes(rt, deps.Devices)
	}
	if deps.Incidents != nil {
		incidentRoutes(rt, deps.Incidents)
	}
	return withRequest(logger, cleanPaths(logger, rt.mux))
}

type router struct {
	mux    *http.ServeMux
	logger *slog.Logger
}

// route registers one handler per method plus a methodless fallback for 405.
// ServeMux prefers the method-specific pattern, and a GET pattern also matches
// HEAD; routes whose Allow value omits HEAD keep rejecting it.
func (rt router) route(path, allow string, methods map[string]http.HandlerFunc) {
	notAllowed := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeJSON(rt.logger, w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	}
	rt.mux.HandleFunc(path, notAllowed)
	allowHead := strings.Contains(allow, http.MethodHead)
	for method, handler := range methods {
		if method == http.MethodGet && !allowHead {
			get := handler
			handler = func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					notAllowed(w, r)
					return
				}
				get(w, r)
			}
		}
		rt.mux.HandleFunc(method+" "+path, handler)
	}
}

// cleanPaths answers non-canonical paths with JSON 404; ServeMux would otherwise
// send an HTML redirect, and these paths were never part of the API.
func cleanPaths(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		clean := path.Clean(p)
		if strings.HasSuffix(p, "/") && clean != "/" {
			clean += "/"
		}
		if !strings.HasPrefix(p, "/") || clean != p {
			writeJSON(logger, w, http.StatusNotFound, errNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func serveReady(logger *slog.Logger, database ReadinessChecker, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	if database == nil || database.Ping(ctx) != nil {
		// Keep driver errors and connection details out of logs and responses.
		logger.Warn("PostgreSQL readiness check failed", "request_id", requestID(r.Context()),
			"error_code", "DATABASE_UNAVAILABLE")
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
	writeStatus(logger, w, r, "ready")
}

func writeStatus(logger *slog.Logger, w http.ResponseWriter, r *http.Request, status string) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(logger, w, http.StatusOK, successResponse{Data: statusData{Status: status}})
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
