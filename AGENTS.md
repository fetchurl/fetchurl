# Agent Guidelines

## Error Handling

- **Never ignore errors:** You must NEVER leave an empty catch block or ignore an error return (e.g., `_ = f.Close()`).
- **Unified Error Reporting:** All unexpected errors must be funneled through a centralized error-reporting function (`util.ReportError` in Go).
    - Do not call `slog.Error` directly for unexpected errors.
    - Do not call `console.error` directly in JS/TS.
