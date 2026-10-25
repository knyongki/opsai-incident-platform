package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/incident"
)

type incidentStub struct {
	IncidentService
	calls   int
	err     error
	input   incident.Input
	bounded bool
}

func (s *incidentStub) Create(ctx context.Context, in incident.Input) (incident.Result, error) {
	s.calls++
	s.input = in
	deadline, ok := ctx.Deadline()
	s.bounded = ok && time.Until(deadline) <= 5*time.Second
	return incident.Result{ID: "12345678-1234-4234-8234-123456789abc", Status: "open", AnalysisStatus: "queued"}, s.err
}

const validIncidentJSON = `{"device_id":"12345678-1234-4234-8234-123456789abc","title":"Docking failed","description":"Stopped","severity":"high"}`

func TestIncidentHTTP(t *testing.T) {
	cases := []struct {
		name, body string
		err        error
		want       int
	}{
		{"valid", validIncidentJSON, nil, 202},
		{"malformed", `{`, nil, 400},
		{"multiple values", validIncidentJSON + ` {}`, nil, 400},
		{"unknown", strings.TrimSuffix(validIncidentJSON, "}") + `,"status":"open"}`, nil, 400},
		{"missing", `{}`, nil, 400},
		{"invalid UUID", strings.Replace(validIncidentJSON, "12345678-1234-4234-8234-123456789abc", "bad", 1), nil, 400},
		{"severity", strings.Replace(validIncidentJSON, "high", "urgent", 1), nil, 400},
		{"null document", `null`, nil, 400},
		{"null title", strings.Replace(validIncidentJSON, `"Docking failed"`, `null`, 1), nil, 400},
		{"null logs", strings.TrimSuffix(validIncidentJSON, "}") + `,"raw_logs":null}`, nil, 400},
		{"null log entry", strings.TrimSuffix(validIncidentJSON, "}") + `,"raw_logs":[null]}`, nil, 400},
		{"bad timestamp", strings.TrimSuffix(validIncidentJSON, "}") + `,"occurred_at":"yesterday"}`, nil, 400},
		{"missing timestamp zone", strings.TrimSuffix(validIncidentJSON, "}") + `,"occurred_at":"2026-01-01T12:00:00"}`, nil, 400},
		{"optional nulls", strings.TrimSuffix(validIncidentJSON, "}") + `,"alarm_code":null,"occurred_at":null}`, nil, 202},
		{"oversize", validIncidentJSON + strings.Repeat(" ", 256*1024), nil, 400},
		{"missing device", validIncidentJSON, incident.ErrDeviceNotFound, 404},
		{"repository failure", validIncidentJSON, incident.ErrRepository, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &incidentStub{err: tc.err}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			h := WithIncidents(logger, stub, http.NotFoundHandler())
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/incidents", strings.NewReader(tc.body)))
			if w.Code != tc.want || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
			if tc.want == 400 && stub.calls != 0 {
				t.Fatal("bad input reached service")
			}
			if stub.calls > 0 && !stub.bounded {
				t.Fatal("missing deadline")
			}
			var envelope map[string]json.RawMessage
			if json.Unmarshal(w.Body.Bytes(), &envelope) != nil {
				t.Fatal("invalid JSON")
			}
			if tc.want == 202 {
				var result incident.Result
				if json.Unmarshal(envelope["data"], &result) != nil || result.Status != "open" || result.AnalysisStatus != "queued" {
					t.Fatal("wrong success envelope")
				}
			} else if _, ok := envelope["error"]; !ok {
				t.Fatal("missing error envelope")
			}
			if strings.Contains(w.Body.String(), "storage failure") {
				t.Fatal("internal error exposed")
			}
		})
	}
}

func TestIncidentRouting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	stub := &incidentStub{}
	h := WithIncidents(logger, stub, NewHandler(logger, nil))
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"PUT", "/api/v1/incidents", 405}, {"POST", "/api/v1/incidents/", 404}, {"GET", "/health", 200}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET, POST" {
			t.Fatal("Allow header")
		}
	}
	if stub.calls != 0 {
		t.Fatal("unexpected creation")
	}
}
