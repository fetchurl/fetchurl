package errutil

import (
	"log/slog"
)

// LogMsg logs the error with a custom message if it is not nil.
func LogMsg(err error, msg string, args ...any) {
	logAt(slog.LevelWarn, logCall{err, msg, args})
}

// ReportError logs an unexpected error.
// It funnels errors through a centralized reporting mechanism (currently slog).
// Future integrations (e.g., Sentry) should be added here.
func ReportError(err error, msg string, args ...any) {
	logAt(slog.LevelError, logCall{err, msg, args})
}

type logCall struct {
	err  error
	msg  string
	args []any
}

func logAt(level slog.Level, call logCall) {
	if call.err == nil {
		return
	}
	allArgs := append([]any{"error", call.err}, call.args...)
	if level >= slog.LevelError {
		slog.Error(call.msg, allArgs...)
	} else {
		slog.Warn(call.msg, allArgs...)
	}
}
