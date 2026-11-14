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

	"github.com/knyongki/opsai-incident-platform/internal/device"
)

// DeviceService is owned by the transport consumer; the domain service satisfies it implicitly.
type DeviceService interface {
	Create(context.Context, device.Input) (device.Device, error)
	List(context.Context) ([]device.Device, error)
	Get(context.Context, string) (device.Device, error)
	Update(context.Context, string, device.Input) (device.Device, error)
	Delete(context.Context, string) error
}

func decodeDevice(w http.ResponseWriter, r *http.Request) (device.Input, error) {
	var in device.Input
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	var body map[string]json.RawMessage
	if err := dec.Decode(&body); err != nil || body == nil {
		return in, device.ErrInvalid
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return in, device.ErrInvalid
	}
	for key, value := range body {
		null := bytes.Equal(bytes.TrimSpace(value), []byte("null"))
		switch key {
		case "name":
			in.Name.Set = true
			if null || json.Unmarshal(value, &in.Name.Value) != nil {
				return in, device.ErrInvalid
			}
		case "type":
			in.Type.Set = true
			if null || json.Unmarshal(value, &in.Type.Value) != nil {
				return in, device.ErrInvalid
			}
		case "status":
			in.Status.Set = true
			if null || json.Unmarshal(value, &in.Status.Value) != nil {
				return in, device.ErrInvalid
			}
		case "location":
			in.Location.Set = true
			if json.Unmarshal(value, &in.Location.Value) != nil {
				return in, device.ErrInvalid
			}
		case "metadata":
			in.Metadata.Set = true
			in.Metadata.Value = value
		default:
			return in, device.ErrInvalid
		}
	}
	return in, nil
}

func deviceRoutes(rt router, svc DeviceService) {
	logger := rt.logger
	// serve gives every device operation one deadline and one error mapping.
	serve := func(status int, call func(context.Context, http.ResponseWriter, *http.Request, string) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			id := r.PathValue("id")
			if id != "" && !device.ValidID(id) {
				deviceError(logger, w, r, device.ErrInvalid)
				return
			}
			result, err := call(ctx, w, r, id)
			if err != nil {
				deviceError(logger, w, r, err)
				return
			}
			if status == http.StatusNoContent {
				w.WriteHeader(status)
				return
			}
			writeJSON(logger, w, status, successResponse{Data: result})
		}
	}
	rt.route("/api/v1/devices", "GET, POST", map[string]http.HandlerFunc{
		http.MethodPost: serve(http.StatusCreated, func(ctx context.Context, w http.ResponseWriter, r *http.Request, _ string) (any, error) {
			in, err := decodeDevice(w, r)
			if err != nil {
				return nil, err
			}
			d, err := svc.Create(ctx, in)
			if err == nil {
				w.Header().Set("Location", "/api/v1/devices/"+d.ID)
			}
			return d, err
		}),
		http.MethodGet: serve(http.StatusOK, func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ string) (any, error) {
			return svc.List(ctx)
		}),
	})
	rt.route("/api/v1/devices/{id}", "GET, PATCH, DELETE", map[string]http.HandlerFunc{
		http.MethodGet: serve(http.StatusOK, func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, id string) (any, error) {
			return svc.Get(ctx, id)
		}),
		http.MethodPatch: serve(http.StatusOK, func(ctx context.Context, w http.ResponseWriter, r *http.Request, id string) (any, error) {
			in, err := decodeDevice(w, r)
			if err != nil {
				return nil, err
			}
			return svc.Update(ctx, id, in)
		}),
		http.MethodDelete: serve(http.StatusNoContent, func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, id string) (any, error) {
			return nil, svc.Delete(ctx, id)
		}),
	})
}

func deviceError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	message := "An internal error occurred."
	switch {
	case errors.Is(err, device.ErrInvalid):
		status = http.StatusBadRequest
		code = "VALIDATION_ERROR"
		message = "The request contains invalid device fields or ID."
	case errors.Is(err, device.ErrNotFound):
		status = http.StatusNotFound
		code = "NOT_FOUND"
		message = "The device was not found."
	case errors.Is(err, device.ErrConflict):
		status = http.StatusConflict
		code = "CONFLICT"
		message = "The device conflicts with an existing record."
	default:
		logger.Error("device request failed", "request_id", requestID(r.Context()), "method", r.Method,
			"resource", "devices", "error_code", code)
	}
	writeJSON(logger, w, status, errorResponse{apiError{Code: code, Message: message}})
}
