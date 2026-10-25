package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/knyongki/opsai-incident-platform/internal/incident"
)

type IncidentService interface {
	Create(context.Context, incident.Input) (incident.Result, error)
}

// WithIncidents adds the single authorized incident route to the existing handler.
func WithIncidents(logger *slog.Logger, svc IncidentService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/incidents" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSON(logger, w, http.StatusMethodNotAllowed, errorResponse{apiError{Code: "METHOD_NOT_ALLOWED", Message: "The request method is not allowed."}})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		in, err := decodeIncident(w, r)
		var result incident.Result
		if err == nil {
			result, err = svc.Create(ctx, in)
		}
		if err != nil {
			status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred."
			switch {
			case errors.Is(err, incident.ErrInvalid):
				status, code, message = http.StatusBadRequest, "VALIDATION_ERROR", "The request contains invalid incident fields."
			case errors.Is(err, incident.ErrDeviceNotFound):
				status, code, message = http.StatusNotFound, "NOT_FOUND", "The referenced device was not found."
			default:
				logger.Error("incident request failed", "error_code", code)
			}
			writeJSON(logger, w, status, errorResponse{apiError{Code: code, Message: message}})
			return
		}
		writeJSON(logger, w, http.StatusAccepted, successResponse{Data: result})
	})
}

func decodeIncident(w http.ResponseWriter, r *http.Request) (incident.Input, error) {
	var in incident.Input
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
	if err != nil || !utf8.Valid(data) {
		return in, incident.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return in, incident.ErrInvalid
	}
	for key, value := range fields {
		null := bytes.Equal(bytes.TrimSpace(value), []byte("null"))
		switch key {
		case "device_id", "title", "description", "severity":
			var text string
			if null || json.Unmarshal(value, &text) != nil {
				return in, incident.ErrInvalid
			}
			switch key {
			case "device_id":
				in.DeviceID = text
			case "title":
				in.Title = text
			case "description":
				in.Description = text
			case "severity":
				in.Severity = text
			}
		case "alarm_code":
			if json.Unmarshal(value, &in.AlarmCode) != nil {
				return in, incident.ErrInvalid
			}
		case "raw_logs":
			var entries []*string
			if null || json.Unmarshal(value, &entries) != nil {
				return in, incident.ErrInvalid
			}
			for _, entry := range entries {
				if entry == nil {
					return in, incident.ErrInvalid
				}
				in.RawLogs = append(in.RawLogs, *entry)
			}
		case "occurred_at":
			if json.Unmarshal(value, &in.OccurredAt) != nil {
				return in, incident.ErrInvalid
			}
		default:
			return in, incident.ErrInvalid
		}
	}
	return in, incident.Validate(in)
}
