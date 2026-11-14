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

func logEntries(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestRequestID(t *testing.T) {
	h := NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), Dependencies{})
	for _, tc := range []struct{ name, inbound string }{
		{"generated", ""},
		{"kept", "client-trace_01.A"},
		{"invalid replaced", "bad id\nwith newline"},
		{"oversized replaced", strings.Repeat("a", 65)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/health", nil)
			if tc.inbound != "" {
				req.Header.Set("X-Request-ID", tc.inbound)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			got := rr.Header().Get("X-Request-ID")
			if tc.name == "kept" {
				if got != tc.inbound {
					t.Fatalf("X-Request-ID = %q, want inbound value", got)
				}
				return
			}
			if got == tc.inbound || len(got) != 32 || !validRequestID.MatchString(got) {
				t.Fatalf("X-Request-ID = %q, want a generated 32-character hex ID", got)
			}
		})
	}
	first, second := httptest.NewRecorder(), httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", "/missing", nil))
	h.ServeHTTP(second, httptest.NewRequest("GET", "/missing", nil))
	if first.Header().Get("X-Request-ID") == second.Header().Get("X-Request-ID") {
		t.Error("generated request IDs repeat")
	}
}

func TestAccessLog(t *testing.T) {
	var logs bytes.Buffer
	h := NewHandler(slog.New(slog.NewJSONHandler(&logs, nil)), Dependencies{})
	req := httptest.NewRequest("GET", "/missing?token=private-query-value", nil)
	req.Header.Set("X-Request-ID", "trace-1")
	req.Header.Set("Authorization", "Bearer private-header-value")
	h.ServeHTTP(httptest.NewRecorder(), req)
	entries := logEntries(t, &logs)
	if len(entries) != 1 {
		t.Fatalf("want one access log line, got %d", len(entries))
	}
	e := entries[0]
	if e["msg"] != "HTTP request" || e["request_id"] != "trace-1" || e["method"] != "GET" ||
		e["path"] != "/missing" || e["status"] != float64(404) {
		t.Errorf("unexpected access log: %v", e)
	}
	if _, ok := e["duration_ms"]; !ok {
		t.Error("access log is missing duration_ms")
	}
	if strings.Contains(logs.String(), "private-") {
		t.Error("access log leaked a query string or header")
	}
}

func TestPanicRecovery(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	calls := 0
	h := withRequest(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			panic("private panic value")
		}
		writeJSON(logger, w, http.StatusOK, successResponse{Data: statusData{Status: "ok"}})
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/boom", nil))
	if rr.Code != 500 || strings.TrimSpace(rr.Body.String()) !=
		`{"error":{"code":"INTERNAL_ERROR","message":"An internal error occurred."}}` {
		t.Fatalf("unexpected panic response: %d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Request-ID") == "" {
		t.Error("panic response is missing X-Request-ID")
	}
	entries := logEntries(t, &logs)
	if len(entries) != 2 || entries[0]["error_code"] != "PANIC" || entries[1]["status"] != float64(500) {
		t.Fatalf("unexpected logs: %s", logs.String())
	}
	if entries[0]["request_id"] != rr.Header().Get("X-Request-ID") {
		t.Error("panic log is missing the request ID")
	}
	if strings.Contains(logs.String(), "private panic value") {
		t.Error("panic value was logged")
	}
	next := httptest.NewRecorder()
	h.ServeHTTP(next, httptest.NewRequest("GET", "/ok", nil))
	if next.Code != 200 {
		t.Fatalf("handler did not recover for the next request: %d", next.Code)
	}
}

func TestPanicAfterWriteAborts(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := withRequest(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":`))
		panic("late failure")
	}))
	rr := httptest.NewRecorder()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("recover() = %v, want http.ErrAbortHandler", got)
		}
		if rr.Body.String() != `{"data":` {
			t.Errorf("partial response was rewritten: %q", rr.Body.String())
		}
		if !strings.Contains(logs.String(), `"error_code":"PANIC"`) {
			t.Error("late panic was not logged")
		}
	}()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/late", nil))
}

func TestAbortHandlerPropagates(t *testing.T) {
	var logs bytes.Buffer
	h := withRequest(slog.New(slog.NewJSONHandler(&logs, nil)), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if got := recover(); !errors.Is(got.(error), http.ErrAbortHandler) {
			t.Fatalf("recover() = %v, want http.ErrAbortHandler", got)
		}
		if logs.Len() != 0 {
			t.Errorf("deliberate abort was logged: %s", logs.String())
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/abort", nil))
}

func TestStatusRecorderUnwrap(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK}
	if err := http.NewResponseController(rec).Flush(); err != nil {
		t.Fatalf("ResponseController cannot reach the underlying writer: %v", err)
	}
	if !rr.Flushed {
		t.Error("flush did not reach the underlying writer")
	}
}
