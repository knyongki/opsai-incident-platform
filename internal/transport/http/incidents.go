package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/knyongki/opsai-incident-platform/internal/incident"
)

type IncidentService interface {
	Create(context.Context, incident.Input) (incident.Result, error)
	List(context.Context, incident.ListOptions) (incident.ListResult, error)
	Get(context.Context, string) (incident.Detail, error)
}

// WithIncidents handles incident creation and read-only collection/detail routes.
func WithIncidents(logger *slog.Logger, svc IncidentService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const path = "/api/v1/incidents"
		collection := r.URL.Path == path
		id := strings.TrimPrefix(r.URL.Path, path+"/")
		detail := strings.HasPrefix(r.URL.Path, path+"/") && id != "" && !strings.Contains(id, "/")
		if !collection && !detail {
			next.ServeHTTP(w, r)
			return
		}
		allowed := "GET"
		if collection {
			allowed = "GET, POST"
		}
		if r.Method != http.MethodGet && !(collection && r.Method == http.MethodPost) {
			w.Header().Set("Allow", allowed)
			writeJSON(logger, w, http.StatusMethodNotAllowed, errorResponse{apiError{Code: "METHOD_NOT_ALLOWED", Message: "The request method is not allowed."}})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var result any
		var err error
		status := http.StatusOK
		switch {
		case r.Method == http.MethodPost:
			var in incident.Input
			in, err = decodeIncident(w, r)
			if err == nil {
				result, err = svc.Create(ctx, in)
			}
			status = http.StatusAccepted
		case collection:
			var options incident.ListOptions
			options, err = parseIncidentList(r.URL.RawQuery)
			if err == nil {
				result, err = svc.List(ctx, options)
			}
		default:
			if r.URL.RawQuery != "" || !incident.ValidID(id) {
				err = incident.ErrInvalid
			} else {
				result, err = svc.Get(ctx, id)
			}
		}
		if err != nil {
			incidentError(logger, w, err)
			return
		}
		writeJSON(logger, w, status, successResponse{Data: result})
	})
}

func incidentError(logger *slog.Logger, w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred."
	switch {
	case errors.Is(err, incident.ErrInvalid):
		status, code, message = http.StatusBadRequest, "VALIDATION_ERROR", "The request contains invalid incident fields, query, or ID."
	case errors.Is(err, incident.ErrDeviceNotFound):
		status, code, message = http.StatusNotFound, "NOT_FOUND", "The referenced device was not found."
	case errors.Is(err, incident.ErrNotFound):
		status, code, message = http.StatusNotFound, "NOT_FOUND", "The incident was not found."
	default:
		logger.Error("incident request failed", "error_code", code)
	}
	writeJSON(logger, w, status, errorResponse{apiError{Code: code, Message: message}})
}

func parseIncidentList(raw string) (incident.ListOptions, error) {
	options := incident.ListOptions{Limit: 20}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return options, incident.ErrInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return options, incident.ErrInvalid
		}
		value := entries[0]
		if value == "" && key != "alarm_code" {
			return options, incident.ErrInvalid
		}
		switch key {
		case "limit":
			for _, c := range value {
				if c < '0' || c > '9' {
					return options, incident.ErrInvalid
				}
			}
			options.Limit, err = strconv.Atoi(value)
			if err != nil || options.Limit < 1 || options.Limit > 100 {
				return options, incident.ErrInvalid
			}
		case "cursor":
			options.Cursor = value
		case "device_id":
			options.DeviceID = value
		case "status":
			options.Status = value
		case "severity":
			options.Severity = value
		case "alarm_code":
			options.AlarmCode = &value
		default:
			return options, incident.ErrInvalid
		}
	}
	return incident.ValidateList(options)
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
