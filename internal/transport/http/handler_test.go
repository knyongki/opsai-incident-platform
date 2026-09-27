package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	handler := NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)))
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
