package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandler(t *testing.T) {
	handler := NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), pingFunc(func(context.Context) error { return nil }))
	for _, tt := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"health", "GET", "/health", `{"data":{"status":"ok"}}`, 200},
		{"ready", "GET", "/ready", `{"data":{"status":"ready"}}`, 200},
		{"query", "GET", "/health?check=1", `{"data":{"status":"ok"}}`, 200},
		{"unknown", "GET", "/missing", `{"error":{"code":"NOT_FOUND","message":"The requested resource was not found."}}`, 404},
		{"root", "GET", "/", `{"error":{"code":"NOT_FOUND","message":"The requested resource was not found."}}`, 404},
		{"trailing slash", "GET", "/health/", `{"error":{"code":"NOT_FOUND","message":"The requested resource was not found."}}`, 404},
		{"health method", "POST", "/health", `{"error":{"code":"METHOD_NOT_ALLOWED","message":"The request method is not allowed."}}`, 405},
		{"ready method", "DELETE", "/ready", `{"error":{"code":"METHOD_NOT_ALLOWED","message":"The request method is not allowed."}}`, 405},
		{"head", "HEAD", "/health", "", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))
			if rr.Code != tt.status {
				t.Fatalf("status = %d, want %d", rr.Code, tt.status)
			}
			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q", got)
			}
			if got := strings.TrimSpace(rr.Body.String()); got != tt.body {
				t.Errorf("body = %s, want %s", got, tt.body)
			}
			if tt.status == 405 && rr.Header().Get("Allow") != "GET, HEAD" {
				t.Error("missing Allow header")
			}
		})
	}
}

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReadiness(t *testing.T) {
	for _, tt := range []struct {
		name, method, path string
		pingError          error
		status, calls      int
	}{
		{"reachable", "GET", "/ready", nil, 200, 1},
		{"unavailable", "GET", "/ready", errors.New("example-private-connection-detail"), 503, 1},
		{"health independent", "GET", "/health", errors.New("offline"), 200, 0},
		{"head reachable", "HEAD", "/ready", nil, 200, 1},
		{"head unavailable", "HEAD", "/ready", errors.New("offline"), 503, 1},
		{"method checked first", "POST", "/ready", nil, 405, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			database := pingFunc(func(ctx context.Context) error {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > readinessTimeout {
					t.Error("readiness ping must have a bounded deadline")
				}
				return tt.pingError
			})
			rr := httptest.NewRecorder()
			NewHandler(logger, database).ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))
			if rr.Code != tt.status || calls != tt.calls {
				t.Fatalf("status = %d, calls = %d; want %d, %d", rr.Code, calls, tt.status, tt.calls)
			}
			if rr.Header().Get("Content-Type") != "application/json" {
				t.Error("missing JSON content type")
			}
			if tt.method == "HEAD" && rr.Body.Len() != 0 {
				t.Error("HEAD returned a body")
			}
			if tt.status == 503 && tt.method == "GET" && strings.TrimSpace(rr.Body.String()) != `{"error":{"code":"DATABASE_UNAVAILABLE","message":"The database is unavailable."}}` {
				t.Errorf("unexpected error envelope: %s", rr.Body.String())
			}
			if strings.Contains(logs.String()+rr.Body.String(), "example-private-connection-detail") {
				t.Error("connection details leaked")
			}
		})
	}
}

func TestReadinessCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checker := pingFunc(func(ctx context.Context) error {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Error("request cancellation was not propagated")
		}
		return ctx.Err()
	})
	rr := httptest.NewRecorder()
	NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), checker).ServeHTTP(rr,
		httptest.NewRequest("GET", "/ready", nil).WithContext(ctx))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestReadinessMissingChecker(t *testing.T) {
	rr := httptest.NewRecorder()
	NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil).ServeHTTP(rr,
		httptest.NewRequest("GET", "/ready", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestWriteJSONEncodingFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	rr := httptest.NewRecorder()
	writeJSON(logger, rr, http.StatusOK, make(chan int))
	var response errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 500 || response.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("unexpected response: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(logs.String(), "encode HTTP response") {
		t.Error("encoding failure was not logged")
	}
}

type failedWriter struct{ httptest.ResponseRecorder }

func (w *failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("connection closed")
}

func TestWriteJSONWriteFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	w := &failedWriter{*httptest.NewRecorder()}
	writeJSON(logger, w, 200, successResponse{Data: statusData{Status: "ok"}})
	if !strings.Contains(logs.String(), "write HTTP response") {
		t.Error("write failure was not logged")
	}
}
