# 2026-01-26 - Fix ignored viper.BindPFlag errors

## Issue

Calls to `viper.BindPFlag` in `cmd/fetchurl/server.go` return an error which is
currently ignored. This can lead to silent failures where CLI flags are not
correctly bound to configuration.

## Root Cause

Missing error check on `viper.BindPFlag`.

## Solution

Wrap `viper.BindPFlag` calls in a helper function `mustBindPFlag` that checks
for errors. If an error occurs, it logs the error using `slog.Error` and
terminates the application with `os.Exit(1)`.

## Pattern

Error Handling / Configuration
- 2026-06-10: Replace bare panic() calls during startup with errutil.ReportError followed by os.Exit(1) to ensure centralized error handling.
