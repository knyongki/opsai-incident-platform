package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knyongki/opsai-incident-platform/internal/device"
)

func TestRouterContract(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	devices := &deviceRepoStub{}
	incidents := &incidentReadStub{}
	h := NewHandler(logger, Dependencies{
		Database:  pingFunc(func(context.Context) error { return nil }),
		Devices:   device.NewService(devices),
		Incidents: incidents,
	})
	for _, tc := range []struct {
		method, path string
		status       int
		code, allow  string
	}{
		{"GET", "//health", 404, "NOT_FOUND", ""},
		{"GET", "/api/v1/../health", 404, "NOT_FOUND", ""},
		{"GET", "/api/v1/./devices", 404, "NOT_FOUND", ""},
		{"GET", "/api/v1/devices/", 404, "NOT_FOUND", ""},
		{"GET", "/api/v1/devices/" + deviceID + "/extra", 404, "NOT_FOUND", ""},
		{"HEAD", "/api/v1/devices", 405, "", "GET, POST"},
		{"HEAD", "/api/v1/devices/" + deviceID, 405, "", "GET, PATCH, DELETE"},
		{"HEAD", "/api/v1/incidents", 405, "", "GET, POST"},
		{"HEAD", "/api/v1/incidents/" + deviceID, 405, "", "GET"},
		{"POST", "/api/v1/devices/" + deviceID, 405, "METHOD_NOT_ALLOWED", "GET, PATCH, DELETE"},
		{"DELETE", "/api/v1/devices", 405, "METHOD_NOT_ALLOWED", "GET, POST"},
		{"PATCH", "/api/v1/incidents/" + deviceID, 405, "METHOD_NOT_ALLOWED", "GET"},
		{"HEAD", "/ready", 200, "", ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			devices.calls, incidents.calls = 0, 0
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
			if rr.Code != tc.status || rr.Header().Get("Allow") != tc.allow {
				t.Fatalf("status = %d, Allow = %q; want %d, %q", rr.Code, rr.Header().Get("Allow"), tc.status, tc.allow)
			}
			if rr.Header().Get("Content-Type") != "application/json" {
				t.Error("missing JSON content type")
			}
			if devices.calls+incidents.calls != 0 {
				t.Error("rejected request reached a service")
			}
			if tc.code == "" {
				return
			}
			var body errorResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.Error.Code != tc.code {
				t.Errorf("body = %s, want code %s", rr.Body.String(), tc.code)
			}
		})
	}
}

func TestErrorLogsIncludeRequestID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := NewHandler(logger, Dependencies{
		Devices:   device.NewService(&deviceRepoStub{err: device.ErrRepository}),
		Incidents: &incidentReadStub{err: io.ErrUnexpectedEOF},
	})
	for _, path := range []string{"/api/v1/devices", "/api/v1/incidents", "/ready"} {
		logs.Reset()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-Request-ID", "trace-42")
		h.ServeHTTP(httptest.NewRecorder(), req)
		found := false
		for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) == nil && entry["level"] != "INFO" {
				found = entry["request_id"] == "trace-42"
			}
		}
		if !found {
			t.Errorf("%s: error log is missing request_id: %s", path, logs.String())
		}
	}
}
