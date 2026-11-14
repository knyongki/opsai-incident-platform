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
	"time"
	"unicode/utf8"

	"github.com/knyongki/opsai-incident-platform/internal/incident"
)

type IncidentService interface {
	Create(context.Context, incident.Input) (incident.Result, error)
	List(context.Context, incident.ListOptions) (incident.ListResult, error)
	Get(context.Context, string) (incident.Detail, error)
}

func incidentRoutes(rt router, svc IncidentService) {
	logger := rt.logger
	serve := func(status int, call func(context.Context, http.ResponseWriter, *http.Request) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			result, err := call(ctx, w, r)
			if err != nil {
				incidentError(logger, w, r, err)
				return
			}
			writeJSON(logger, w, status, successResponse{Data: result})
		}
	}
	rt.route("/api/v1/incidents", "GET, POST", map[string]http.HandlerFunc{
		http.MethodPost: serve(http.StatusAccepted, func(ctx context.Context, w http.ResponseWriter, r *http.Request) (any, error) {
			in, err := decodeIncident(w, r)
			if err != nil {
				return nil, err
			}
			return svc.Create(ctx, in)
		}),
		http.MethodGet: serve(http.StatusOK, func(ctx context.Context, _ http.ResponseWriter, r *http.Request) (any, error) {
			options, err := parseIncidentList(r.URL.RawQuery)
			if err != nil {
				return nil, err
			}
			return svc.List(ctx, options)
		}),
	})
	rt.route("/api/v1/incidents/{id}", "GET", map[string]http.HandlerFunc{
		http.MethodGet: serve(http.StatusOK, func(ctx context.Context, _ http.ResponseWriter, r *http.Request) (any, error) {
			id := r.PathValue("id")
			if r.URL.RawQuery != "" || !incident.ValidID(id) {
				return nil, incident.ErrInvalid
			}
			return svc.Get(ctx, id)
		}),
	})
}

func incidentError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred."
	switch {
	case errors.Is(err, incident.ErrInvalid):
		status, code, message = http.StatusBadRequest, "VALIDATION_ERROR", "The request contains invalid incident fields, query, or ID."
	case errors.Is(err, incident.ErrDeviceNotFound):
		status, code, message = http.StatusNotFound, "NOT_FOUND", "The referenced device was not found."
	case errors.Is(err, incident.ErrNotFound):
		status, code, message = http.StatusNotFound, "NOT_FOUND", "The incident was not found."
	default:
		logger.Error("incident request failed", "request_id", requestID(r.Context()), "error_code", code)
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
