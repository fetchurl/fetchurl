package errutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
)

var boomErr = errors.New("boom")

func TestLogMsgAndReportErrorNilSilent(t *testing.T) {
	var buf bytes.Buffer
	restore := setTestLogger(t, &buf)

	LogMsg(nil, "warn should not appear", "k", "v")
	ReportError(nil, "error should not appear", "k", "v")
	restore()

	if buf.Len() != 0 {
		t.Fatalf("logged on nil error: %s", buf.String())
	}
}

func TestLogMsgWarnsWithErrorAndArgs(t *testing.T) {
	got := captureRecord(t, func() {
		LogMsg(boomErr, "failed op", "path", "/tmp/x")
	})
	if got["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", got["level"])
	}
	if got["msg"] != "failed op" {
		t.Fatalf("msg = %v, want failed op", got["msg"])
	}
	if got["error"] != "boom" {
		t.Fatalf("error = %v, want boom", got["error"])
	}
	if got["path"] != "/tmp/x" {
		t.Fatalf("path = %v, want /tmp/x", got["path"])
	}
}

func TestReportErrorLogsErrorLevel(t *testing.T) {
	got := captureRecord(t, func() {
		ReportError(boomErr, "unexpected", "url", "https://example")
	})
	if got["level"] != "ERROR" {
		t.Fatalf("level = %v, want ERROR", got["level"])
	}
	if got["msg"] != "unexpected" {
		t.Fatalf("msg = %v, want unexpected", got["msg"])
	}
	if got["error"] != "boom" {
		t.Fatalf("error = %v, want boom", got["error"])
	}
	if got["url"] != "https://example" {
		t.Fatalf("url = %v, want https://example", got["url"])
	}
}

func captureRecord(t *testing.T, fn func()) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	restore := setTestLogger(t, &buf)
	fn()
	restore()

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode log: %v\nraw: %s", err, buf.String())
	}
	return got
}

func setTestLogger(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	prev := slog.Default()
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	return func() { slog.SetDefault(prev) }
}
