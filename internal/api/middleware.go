package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type errorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{
		Error:   msg,
		Code:    defaultErrorCode(status),
		Message: msg,
	})
}

func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{
		Error:   msg,
		Code:    code,
		Message: msg,
	})
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	if err == nil {
		writeError(w, status, http.StatusText(status))
		return
	}
	if apiErr, ok := err.(*types.APIError); ok {
		writeJSON(w, status, errorResponse{
			Error:   apiErr.Message,
			Code:    apiErr.Code,
			Message: apiErr.Message,
		})
		return
	}
	writeError(w, status, err.Error())
}

// responseRecorder wraps http.ResponseWriter to capture status code and body size.
type responseRecorder struct {
	http.ResponseWriter
	status int
	size   int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.size += n
	return n, err
}

// Flush forwards to the underlying ResponseWriter when it implements
// http.Flusher.  This preserves SSE streaming through the request-logging
// middleware.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestLogger is a chi-compatible middleware that logs every HTTP request
// and its response to the structured logger.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Skip logging for the log-polling endpoint to avoid noise.
		if r.URL.Path == "/api/v1/logs" && r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}

		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

		// Capture request body for mutation methods (POST, PUT, PATCH).
		var bodySnippet string
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			if r.ContentLength > 0 && r.ContentLength < 4096 {
				buf := new(bytes.Buffer)
				buf.ReadFrom(r.Body)
				bodySnippet = buf.String()
				// Put the body back so the handler can read it.
				r.Body = http.NoBody
				r.Body = nopCloser{bytes.NewReader(buf.Bytes())}
			}
		}

		next.ServeHTTP(rec, r)

		duration := time.Since(start)
		fields := []string{
			"method", r.Method,
			"path", r.URL.Path,
			"status", http.StatusText(rec.status),
			"status_code", itoa(rec.status),
			"duration_ms", itoa(int(duration.Milliseconds())),
			"bytes", itoa(rec.size),
			"remote", r.RemoteAddr,
		}
		if r.URL.RawQuery != "" {
			fields = append(fields, "query", r.URL.RawQuery)
		}
		if bodySnippet != "" {
			fields = append(fields, "body", bodySnippet)
		}

		msg := r.Method + " " + r.URL.Path
		if rec.status >= 500 {
			logger.Error("api", msg, fields...)
		} else if rec.status >= 400 {
			logger.Warn("api", msg, fields...)
		} else {
			logger.Info("api", msg, fields...)
		}
	})
}

// nopCloser pairs an io.Reader with a no-op Close method.
type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func (s *Server) withRequestBodyLimit(next http.HandlerFunc) http.HandlerFunc {
	return s.withBodyLimit(s.maxRequestBodyBytes, next)
}

func (s *Server) withUploadBodyLimit(next http.HandlerFunc) http.HandlerFunc {
	return s.withBodyLimit(s.maxUploadBodyBytes, next)
}

func (s *Server) withBodyLimit(limit int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if limit > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next(w, r)
	}
}

func isRequestTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return true
	}
	return strings.Contains(err.Error(), "http: request body too large")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func defaultErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "resource_not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		return "internal_error"
	}
}
