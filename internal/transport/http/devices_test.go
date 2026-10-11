package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knyongki/opsai-incident-platform/internal/device"
)

const deviceID = "6c5738ce-b611-4b6f-9154-3abc9f5b8bb4"

type deviceRepoStub struct {
	err   error
	calls int
	input device.Input
}

func (s *deviceRepoStub) Create(_ context.Context, d device.Device) (device.Device, error) {
	s.calls++
	return d, s.err
}
func (s *deviceRepoStub) List(context.Context) ([]device.Device, error) {
	s.calls++
	return []device.Device{{ID: deviceID, Metadata: json.RawMessage(`{}`)}}, s.err
}
func (s *deviceRepoStub) Get(context.Context, string) (device.Device, error) {
	s.calls++
	return device.Device{ID: deviceID, Metadata: json.RawMessage(`{}`)}, s.err
}
func (s *deviceRepoStub) Update(_ context.Context, _ string, in device.Input) (device.Device, error) {
	s.calls++
	s.input = in
	return device.Device{ID: deviceID, Name: in.Name.Value, Metadata: json.RawMessage(`{}`)}, s.err
}
func (s *deviceRepoStub) Delete(context.Context, string) error { s.calls++; return s.err }
func TestDeviceHandlers(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		err                      error
		status                   int
	}{
		{"create", "POST", "/api/v1/devices", `{"name":"AMR-07","type":"amr"}`, nil, 201},
		{"list", "GET", "/api/v1/devices", "", nil, 200},
		{"get", "GET", "/api/v1/devices/" + deviceID, "", nil, 200},
		{"patch", "PATCH", "/api/v1/devices/" + deviceID, `{"name":"AMR-08","location":null}`, nil, 200},
		{"delete", "DELETE", "/api/v1/devices/" + deviceID, "", nil, 204},
		{"malformed", "POST", "/api/v1/devices", `{`, nil, 400},
		{"unknown", "POST", "/api/v1/devices", `{"name":"a","type":"amr","secret":true}`, nil, 400},
		{"patch unknown", "PATCH", "/api/v1/devices/" + deviceID, `{"id":"other"}`, nil, 400},
		{"empty", "POST", "/api/v1/devices", "", nil, 400},
		{"null", "POST", "/api/v1/devices", `null`, nil, 400},
		{"array", "POST", "/api/v1/devices", `[]`, nil, 400},
		{"trailing", "POST", "/api/v1/devices", `{"name":"a","type":"amr"} {}`, nil, 400},
		{"wrong type", "POST", "/api/v1/devices", `{"name":1,"type":"amr"}`, nil, 400},
		{"null field", "POST", "/api/v1/devices", `{"name":null,"type":"amr"}`, nil, 400},
		{"required", "POST", "/api/v1/devices", `{"name":"a"}`, nil, 400},
		{"empty patch", "PATCH", "/api/v1/devices/" + deviceID, `{}`, nil, 400},
		{"oversize", "POST", "/api/v1/devices", `{"name":"` + strings.Repeat("a", 65536) + `","type":"amr"}`, nil, 400},
		{"invalid id", "GET", "/api/v1/devices/nope", "", nil, 400},
		{"invalid patch id", "PATCH", "/api/v1/devices/nope", `{"name":"a"}`, nil, 400},
		{"invalid delete id", "DELETE", "/api/v1/devices/nope", "", nil, 400},
		{"missing get", "GET", "/api/v1/devices/" + deviceID, "", device.ErrNotFound, 404},
		{"missing update", "PATCH", "/api/v1/devices/" + deviceID, `{"name":"a"}`, device.ErrNotFound, 404},
		{"missing delete", "DELETE", "/api/v1/devices/" + deviceID, "", device.ErrNotFound, 404},
		{"conflict", "POST", "/api/v1/devices", `{"name":"a","type":"amr"}`, device.ErrConflict, 409},
		{"failed list", "GET", "/api/v1/devices", "", errors.New("sensitive SQL driver detail"), 500},
		{"failed create", "POST", "/api/v1/devices", `{"name":"a","type":"amr"}`, device.ErrRepository, 500},
		{"failed get", "GET", "/api/v1/devices/" + deviceID, "", device.ErrRepository, 500},
		{"failed patch", "PATCH", "/api/v1/devices/" + deviceID, `{"name":"a"}`, device.ErrRepository, 500},
		{"failed delete", "DELETE", "/api/v1/devices/" + deviceID, "", device.ErrRepository, 500},
		{"method", "PUT", "/api/v1/devices/" + deviceID, `{}`, nil, 405},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &deviceRepoStub{err: tt.err}
			var logs bytes.Buffer
			h := NewHandler(slog.New(slog.NewJSONHandler(&logs, nil)), nil, device.NewService(repo))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if rr.Code != tt.status {
				t.Fatalf("status %d want %d: %s", rr.Code, tt.status, rr.Body.String())
			}
			if tt.status == 400 && repo.calls != 0 {
				t.Fatal("invalid request reached repository")
			}
			if tt.status == 204 {
				if rr.Body.Len() != 0 {
					t.Fatal("204 has body")
				}
				return
			}
			if rr.Header().Get("Content-Type") != "application/json" {
				t.Fatal("missing JSON content type")
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			key := "data"
			if tt.status >= 400 {
				key = "error"
			}
			if _, ok := envelope[key]; !ok {
				t.Fatal("wrong envelope")
			}
			if strings.Contains(rr.Body.String()+logs.String(), "sensitive SQL") {
				t.Fatal("error leaked")
			}
			if tt.name == "create" {
				var d device.Device
				if err := json.Unmarshal(envelope["data"], &d); err != nil {
					t.Fatal(err)
				}
				if d.Name != "AMR-07" || d.Status != "unknown" || !device.ValidID(d.ID) || rr.Header().Get("Location") != "/api/v1/devices/"+d.ID {
					t.Fatal("wrong created device")
				}
			}
			if tt.name == "patch" && (!repo.input.Location.Set || repo.input.Location.Value != nil || repo.input.Type.Set) {
				t.Fatal("patch presence lost")
			}
		})
	}
}
