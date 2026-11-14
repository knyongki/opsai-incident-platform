package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

type requestIDKey struct{}

// Inbound IDs are kept only when short and plain, so a client cannot inject
// arbitrary text into logs or response headers.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error since Go 1.24.
	return hex.EncodeToString(b)
}

// statusRecorder remembers the response status for the access log and whether
// headers were already sent, which decides how a panic can still be answered.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// withRequest assigns a request ID, recovers panics, and writes one access log
// line. Logs never include query strings, headers, bodies, or panic values.
func withRequest(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			recovered := recover()
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			headersSent := rec.wrote
			if recovered != nil {
				logger.Error("HTTP handler panic", "request_id", id, "error_code", "PANIC",
					"stack", string(debug.Stack()))
				if !headersSent {
					writeJSON(logger, rec, http.StatusInternalServerError, errInternal)
				}
			}
			logger.Info("HTTP request", "request_id", id, "method", r.Method, "path", r.URL.Path,
				"status", rec.status, "duration_ms", time.Since(started).Milliseconds())
			if recovered != nil && headersSent {
				// A partly sent response cannot become an error; abort it so the
				// client does not mistake it for a complete one.
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}
