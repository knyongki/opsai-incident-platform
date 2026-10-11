package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

func serveDevice(logger *slog.Logger, svc DeviceService, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/devices")
	collection := id == ""
	if !collection {
		if !strings.HasPrefix(id, "/") || strings.Contains(id[1:], "/") {
			deviceError(logger, w, r, device.ErrNotFound)
			return
		}
		id = id[1:]
		if !device.ValidID(id) {
			deviceError(logger, w, r, device.ErrInvalid)
			return
		}
	}
	var result any
	var err error
	status := http.StatusOK
	switch {
	case collection && r.Method == http.MethodPost:
		var in device.Input
		in, err = decodeDevice(w, r)
		if err == nil {
			var d device.Device
			d, err = svc.Create(ctx, in)
			result = d
			if err == nil {
				w.Header().Set("Location", "/api/v1/devices/"+d.ID)
			}
		}
		status = http.StatusCreated
	case collection && r.Method == http.MethodGet:
		result, err = svc.List(ctx)
	case !collection && r.Method == http.MethodGet:
		result, err = svc.Get(ctx, id)
	case !collection && r.Method == http.MethodPatch:
		var in device.Input
		in, err = decodeDevice(w, r)
		if err == nil {
			result, err = svc.Update(ctx, id, in)
		}
	case !collection && r.Method == http.MethodDelete:
		err = svc.Delete(ctx, id)
		status = http.StatusNoContent
	default:
		allow := "GET, POST"
		if !collection {
			allow = "GET, PATCH, DELETE"
		}
		w.Header().Set("Allow", allow)
		writeJSON(logger, w, http.StatusMethodNotAllowed, errorResponse{apiError{Code: "METHOD_NOT_ALLOWED", Message: "The request method is not allowed."}})
		return
	}
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
		logger.Error("device request failed", "method", r.Method, "resource", "devices", "error_code", code)
	}
	writeJSON(logger, w, status, errorResponse{apiError{Code: code, Message: message}})
}
