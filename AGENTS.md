# Agent Guidelines

## Error Handling

- **Never ignore errors:** You must NEVER leave an empty catch block or ignore an error return (e.g., `_ = f.Close()`).
- **Unified Error Reporting:** All unexpected errors must be funneled through a centralized error-reporting function (`errutil.ReportError` in Go).
  - Do not call `slog.Error` directly for unexpected errors.

## Scope

This repository is the **Go reference server/CLI** only. Protocol changes belong in [fetchurl/spec](https://github.com/fetchurl/spec). Client SDKs live in `fetchurl/sdk-*` repos.
