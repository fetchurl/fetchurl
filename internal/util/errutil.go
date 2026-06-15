package util

import (
	"log/slog"
)

// LogMsg logs the error with a custom message if it is not nil.
func LogMsg(err error, msg string, args ...any) {
	if err != nil {
		allArgs := append([]any{"error", err}, args...)
		slog.Warn(msg, allArgs...)
	}
}

// ReportError logs an unexpected error.
// It funnels errors through a centralized reporting mechanism (currently slog).
// Future integrations (e.g., Sentry) should be added here.
func ReportError(err error, msg string, args ...any) {
	if err != nil {
		allArgs := append([]any{"error", err}, args...)
		slog.Error(msg, allArgs...)
	}
}
