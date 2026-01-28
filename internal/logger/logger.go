package logger

import (
	"context"
	"log/slog"
	"os"
)

const (
	RequestIDKey = "request_id"
	UserIDKey    = "user_id"
)

func New() *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h)
}

func WithRequestID(ctx context.Context, l *slog.Logger, requestID string) *slog.Logger {
	return l.With(slog.String(RequestIDKey, requestID))
}

func WithUserID(l *slog.Logger, userID string) *slog.Logger {
	if userID == "" {
		return l
	}
	return l.With(slog.String(UserIDKey, userID))
}
