package httpapi

import (
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

	"github.com/knyongki/opsai-incident-platform/internal/analysis"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
)

type incidentReadStub struct {
	IncidentService
	options incident.ListOptions
	id      string
	calls   int
	bounded bool
	err     error
	list    incident.ListResult
	detail  incident.Detail
}

func (s *incidentReadStub) observe(ctx context.Context) {
	s.calls++
	deadline, ok := ctx.Deadline()
	s.bounded = ok && time.Until(deadline) <= 5*time.Second
}
func (s *incidentReadStub) List(ctx context.Context, o incident.ListOptions) (incident.ListResult, error) {
	s.observe(ctx)
	s.options = o
	return s.list, s.err
}
func (s *incidentReadStub) Get(ctx context.Context, id string) (incident.Detail, error) {
	s.observe(ctx)
	s.id = id
	return s.detail, s.err
}
func readHandler(s *incidentReadStub) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(logger, Dependencies{Incidents: s})
}
func TestIncidentListQueryValidation(t *testing.T) {
	for _, query := range []string{
		"limit=0", "limit=-1", "limit=101", "limit=x", "limit=1.5", "limit=+1", "limit=",
		"cursor=", "cursor=bad", "device_id=bad", "device_id=", "status=queued", "status=",
		"severity=HIGH", "severity=", "unknown=1", "limit=1&limit=2", "status=open&status=open",
		"device_id=x&device_id=y", "alarm_code=A&alarm_code=B", "cursor=x&cursor=y",
		"severity=low&severity=high", "status=%ZZ", "status=open;severity=high",
		"alarm_code=%00", "alarm_code=%FF", "alarm_code=" + strings.Repeat("x", 101),
	} {
		t.Run(query, func(t *testing.T) {
			s := &incidentReadStub{}
			req := httptest.NewRequest("GET", "/api/v1/incidents", nil)
			req.URL.RawQuery = query
			w := httptest.NewRecorder()
			readHandler(s).ServeHTTP(w, req)
			if w.Code != 400 || s.calls != 0 || !strings.Contains(w.Body.String(), `"code":"VALIDATION_ERROR"`) {
				t.Fatalf("status=%d body=%s calls=%d", w.Code, w.Body.String(), s.calls)
			}
		})
	}
}
func TestIncidentListHTTP(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	for _, tc := range []struct {
		name, query string
		empty       bool
	}{
		{"default", "", true},
		{"filters", "?limit=7&device_id=" + id + "&status=open&severity=high&alarm_code=DOCKING", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := []incident.Summary{}
			if !tc.empty {
				items = append(items, incident.Summary{ID: id, DeviceID: id, Title: "Summary", Status: "open", Severity: "high", AnalysisStatus: "queued"})
			}
			s := &incidentReadStub{list: incident.ListResult{Items: items}}
			w := httptest.NewRecorder()
			readHandler(s).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/incidents"+tc.query, nil))
			if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || !s.bounded {
				t.Fatalf("bad response: %s", w.Body.String())
			}
			if tc.empty {
				if s.options.Limit != 20 || !strings.Contains(w.Body.String(), `"items":[]`) {
					t.Fatal("empty/default contract")
				}
			} else if s.options.Limit != 7 || s.options.DeviceID != id || s.options.Status != "open" || s.options.Severity != "high" || s.options.AlarmCode == nil || *s.options.AlarmCode != "DOCKING" {
				t.Fatal("filters not passed")
			}
			var envelope struct {
				Data struct {
					Items []map[string]any `json:"items"`
					Page  map[string]any   `json:"page"`
				} `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Data.Page["has_more"] != false || envelope.Data.Page["next_cursor"] != nil {
				t.Fatal("page envelope")
			}
			for _, item := range envelope.Data.Items {
				for _, key := range []string{"description", "raw_logs", "job_id", "attempt_count", "last_error", "resolved_at"} {
					if _, exists := item[key]; exists {
						t.Fatalf("list leaks %s", key)
					}
				}
				if len(item) != 10 || item["analysis_status"] != "queued" || item["alarm_code"] != nil || item["occurred_at"] != nil {
					t.Fatal("summary contract")
				}
			}
		})
	}
}
func TestIncidentDetailHTTP(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	s := &incidentReadStub{detail: incident.Detail{Summary: incident.Summary{ID: id, AnalysisStatus: "queued"}, Description: "Full description", RawLogs: []string{"Synthetic log"}}}
	w := httptest.NewRecorder()
	readHandler(s).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/incidents/"+id, nil))
	if w.Code != 200 || s.id != id || !s.bounded {
		t.Fatal("detail not served")
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil {
		t.Fatal("detail JSON")
	}
	d := envelope.Data
	if len(d) != 14 || d["description"] != "Full description" || d["resolved_at"] != nil || d["alarm_code"] != nil || d["occurred_at"] != nil || len(d["raw_logs"].([]any)) != 1 {
		t.Fatal("detail shape")
	}
	if a, ok := d["analysis"]; !ok || a != nil {
		t.Fatal("detail without a stored result must include analysis: null")
	}
	for _, key := range []string{"job_id", "attempt_count", "locked_at", "last_error", "claim_token"} {
		if _, ok := d[key]; ok {
			t.Fatal("internal job field exposed")
		}
	}
}
func TestIncidentDetailHTTPStoredAnalysis(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	analysisID := "87654321-4321-4321-8321-cba987654321"
	created := time.Date(2026, 11, 14, 7, 30, 0, 0, time.UTC)
	s := &incidentReadStub{detail: incident.Detail{
		Summary: incident.Summary{ID: id, AnalysisStatus: "completed"}, RawLogs: []string{},
		Analysis: &incident.Analysis{ID: analysisID, SchemaVersion: 1, Source: "fake", CreatedAt: created, Result: analysis.Result{
			Summary: "Synthetic summary", PrimarySuspectedCause: "No diagnosis was performed",
			ContributingFactors: []string{}, ObservedConditions: []string{"Synthetic input"},
			RecommendedActions: []analysis.Action{}, SafetyNotes: []string{"Test output only"},
			Confidence: 0, Evidence: []analysis.Evidence{{SourceID: id, Claim: "Synthetic incident record"}},
		}},
	}}
	w := httptest.NewRecorder()
	readHandler(s).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/incidents/"+id, nil))
	var envelope struct {
		Data struct {
			AnalysisStatus string `json:"analysis_status"`
			Analysis       *struct {
				ID            string                     `json:"id"`
				SchemaVersion int                        `json:"schema_version"`
				Source        string                     `json:"source"`
				CreatedAt     string                     `json:"created_at"`
				Result        map[string]json.RawMessage `json:"result"`
			} `json:"analysis"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Data.Analysis == nil {
		t.Fatalf("stored analysis not served: %d %s", w.Code, w.Body.String())
	}
	a := envelope.Data.Analysis
	if envelope.Data.AnalysisStatus != "completed" || a.ID != analysisID || a.SchemaVersion != 1 || a.Source != "fake" || a.CreatedAt != "2026-11-14T07:30:00Z" {
		t.Fatalf("analysis metadata: %+v", a)
	}
	for _, key := range []string{"summary", "primary_suspected_cause", "contributing_factors", "observed_conditions",
		"recommended_actions", "safety_notes", "confidence", "evidence"} {
		if _, ok := a.Result[key]; !ok {
			t.Fatalf("result field %s missing", key)
		}
	}
	if len(a.Result) != 8 || string(a.Result["contributing_factors"]) != "[]" || string(a.Result["confidence"]) != "0" {
		t.Fatal("result contract")
	}
}
func TestIncidentReadErrorsAndRouting(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	for _, tc := range []struct {
		method, path string
		err          error
		status       int
		allow        string
		calls        int
	}{
		{"GET", "/api/v1/incidents/bad", nil, 400, "", 0},
		{"GET", "/api/v1/incidents/" + id + "?limit=1", nil, 400, "", 0},
		{"GET", "/api/v1/incidents/" + id + "?bad=%ZZ", nil, 400, "", 0},
		{"GET", "/api/v1/incidents/" + id, incident.ErrNotFound, 404, "", 1},
		{"GET", "/api/v1/incidents/" + id, errors.New("private driver text"), 500, "", 1},
		{"GET", "/api/v1/incidents", errors.New("private driver text"), 500, "", 1},
		{"POST", "/api/v1/incidents/" + id, nil, 405, "GET", 0},
		{"DELETE", "/api/v1/incidents", nil, 405, "GET, POST", 0},
		{"GET", "/api/v1/incidents/" + id + "/analysis", nil, 404, "", 0},
		{"GET", "/api/v1/incidents/", nil, 404, "", 0},
		{"GET", "/api/v1/incidents-extra", nil, 404, "", 0},
	} {
		s := &incidentReadStub{err: tc.err}
		w := httptest.NewRecorder()
		readHandler(s).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || w.Header().Get("Allow") != tc.allow || s.calls != tc.calls || strings.Contains(w.Body.String(), "private driver text") {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"error":`) {
			t.Fatal("missing error envelope")
		}
	}
}
