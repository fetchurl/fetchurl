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
- 2026-06-17: Fix biome and ruff lints by removing unused imports, prefixing unused args with underscore, and untangling assignments from while-loop conditions.
