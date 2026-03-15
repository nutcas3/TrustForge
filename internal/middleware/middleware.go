// Package middleware provides HTTP middleware for the TrustForge API.
package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nutcas3/trustforge/internal/metrics"
	"github.com/sirupsen/logrus"
)

const requestIDHeader = "X-Request-ID"

// Chain applies a sequence of middleware in order (outermost first)
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// RequestID injects a unique request ID into every request header and response
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set(requestIDHeader, id)
		r.Header.Set(requestIDHeader, id)
		next.ServeHTTP(w, r)
	})
}

// Logger logs every request with method, path, status, duration, and request ID
func Logger(logger *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			logger.WithFields(logrus.Fields{
				"method":     r.Method,
				"path":       r.URL.Path,
				"status":     wrapped.statusCode,
				"duration":   time.Since(start).String(),
				"request_id": r.Header.Get(requestIDHeader),
				"remote":     r.RemoteAddr,
			}).Info("http request")
		})
	}
}

// Metrics records Prometheus metrics for every HTTP request
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		metrics.HTTPRequestDuration.WithLabelValues(
			r.Method,
			r.URL.Path,
			strconv.Itoa(wrapped.statusCode),
		).Observe(time.Since(start).Seconds())
	})
}

// Recovery catches panics and returns a 500 rather than crashing the server
func Recovery(logger *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					stack := debug.Stack()
					logger.WithFields(logrus.Fields{
						"panic":      fmt.Sprintf("%v", rec),
						"stack":      string(stack),
						"request_id": r.Header.Get(requestIDHeader),
					}).Error("panic recovered")

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprintf(w, `{"error":"internal server error","request_id":%q}`,
						r.Header.Get(requestIDHeader))
				}
			}()
			next.ServeHTTP(wrapped(w), r)
		})
	}
}

// RateLimit is a simple per-IP token bucket rate limiter
// For production, use golang.org/x/time/rate or an external service
func MaxBodySize(bytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, bytes)
			next.ServeHTTP(w, r)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// wrapped returns a responseWriter if w is not already one
func wrapped(w http.ResponseWriter) http.ResponseWriter {
	if _, ok := w.(*responseWriter); ok {
		return w
	}
	return &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}
