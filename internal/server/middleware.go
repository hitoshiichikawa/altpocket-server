package server

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"log/slog"

	"altpocket/internal/logger"
)

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), logger.RequestIDKey, id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wr := &responseWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(wr, r)
			dur := time.Since(start)
			log.Info("request", slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Int("status", wr.status), slog.Int64("duration_ms", dur.Milliseconds()), slog.String("request_id", requestIDFromContext(r.Context())))
		})
	}
}

func requestIDFromContext(ctx context.Context) string {
	v := ctx.Value(logger.RequestIDKey)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
