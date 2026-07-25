package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fetchurl/fetchurl/internal/app"
	"github.com/fetchurl/fetchurl/internal/errutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// httpShutdownTimeout is how long Shutdown may wait for in-flight handlers
// after the app context has been cancelled.
const httpShutdownTimeout = 15 * time.Second

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Starts the HTTP server",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := serverConfigFromViper()

		server, cleanup, err := app.NewServer(cmd.Context(), cfg)
		if err != nil {
			errutil.ReportError(err, "Failed to initialize server")
			os.Exit(1)
		}

		// SIGINT/SIGTERM → cancel app work, then Shutdown so handlers can drain.
		runCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if err := runHTTPServer(runCtx, server, cleanup); err != nil {
			errutil.ReportError(err, "Server failed")
			os.Exit(1)
		}
	},
}

// runHTTPServer serves until runCtx is cancelled or ListenAndServe fails.
//
// On cancellation it calls cleanup *before* Shutdown. CAS miss handling uses
// the app context (not the request context) for singleflight origin fetches
// and eviction; if that context stays live, long downloads block Shutdown for
// the full httpShutdownTimeout and a timed-out path can os.Exit without a
// clean cancel. Cancelling first lets those goroutines observe ctx.Done while
// Shutdown waits for ServeHTTP to return.
func runHTTPServer(runCtx context.Context, server *http.Server, cleanup func()) error {
	return runHTTPServerServe(runCtx, server, cleanup, server.ListenAndServe)
}

func runHTTPServerServe(runCtx context.Context, server *http.Server, cleanup func(), serve func() error) error {
	if cleanup == nil {
		cleanup = func() {}
	}
	defer cleanup()

	errCh := make(chan error, 1)
	go func() {
		err := serve()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-runCtx.Done():
		// Stop app-scoped work before draining HTTP handlers.
		cleanup()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		if err := <-errCh; err != nil {
			return fmt.Errorf("server after shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}
}

func init() {
	rootCmd.AddCommand(serverCmd)

	// Enable environment variable support with FETCHURL_ prefix
	viper.SetEnvPrefix("FETCHURL")
	viper.AutomaticEnv()

	serverCmd.Flags().Int("port", 8080, "Port to run the server on")
	serverCmd.Flags().String("cache-dir", "./cache", "Directory to store cached files")
	serverCmd.Flags().Int64("max-cache-size", 1024*1024*1024, "Max cache size in bytes (default 1GB)")
	serverCmd.Flags().Int64("min-free-space", 0, "Min free disk space in bytes (if set, overrides max-cache-size)")
	serverCmd.Flags().Duration("eviction-interval", time.Minute, "Interval to check for evictions")
	serverCmd.Flags().String("eviction-strategy", "lru", "Eviction strategy to use (lru)")
	serverCmd.Flags().StringSlice("upstream", []string{}, "Upstream fetchurl servers (repeatable or comma-separated; same for FETCHURL_UPSTREAM)")

	mustBindPFlag("port", serverCmd.Flags().Lookup("port"))
	mustBindPFlag("cache-dir", serverCmd.Flags().Lookup("cache-dir"))
	mustBindPFlag("max-cache-size", serverCmd.Flags().Lookup("max-cache-size"))
	mustBindPFlag("min-free-space", serverCmd.Flags().Lookup("min-free-space"))
	mustBindPFlag("eviction-interval", serverCmd.Flags().Lookup("eviction-interval"))
	mustBindPFlag("eviction-strategy", serverCmd.Flags().Lookup("eviction-strategy"))
	mustBindPFlag("upstream", serverCmd.Flags().Lookup("upstream"))

	// Bind environment variables
	mustBindEnv("port", "FETCHURL_PORT")
	mustBindEnv("cache-dir", "FETCHURL_CACHE_DIR")
	mustBindEnv("max-cache-size", "FETCHURL_MAX_CACHE_SIZE")
	mustBindEnv("min-free-space", "FETCHURL_MIN_FREE_SPACE")
	mustBindEnv("eviction-interval", "FETCHURL_EVICTION_INTERVAL")
	mustBindEnv("eviction-strategy", "FETCHURL_EVICTION_STRATEGY")
	mustBindEnv("upstream", "FETCHURL_UPSTREAM")
}

func serverConfigFromViper() app.Config {
	cfg := app.Config{
		Port:             viper.GetInt("port"),
		CacheDir:         viper.GetString("cache-dir"),
		MaxCacheSize:     viper.GetInt64("max-cache-size"),
		MinFreeSpace:     viper.GetInt64("min-free-space"),
		EvictionInterval: viper.GetDuration("eviction-interval"),
		EvictionStrategy: viper.GetString("eviction-strategy"),
		Upstreams:        configUpstreams(),
	}
	// Flag/env contract: min-free-space, when set, replaces max-cache-size.
	// NewServer enables each policy only when its threshold is > 0; without
	// this, the 1GiB default max-cache-size would still run alongside min-free.
	if cfg.MinFreeSpace > 0 {
		cfg.MaxCacheSize = 0
	}
	return cfg
}

// configUpstreams returns daisy-chain upstream base URLs from --upstream /
// FETCHURL_UPSTREAM.
//
// viper.GetStringSlice uses cast.ToStringSlice, which splits raw env strings on
// whitespace only (strings.Fields). That breaks the usual Docker/k8s form
// FETCHURL_UPSTREAM=url1,url2 (one element) and mangles "url1, url2" into
// "url1," + "url2". Re-normalize with comma splitting so env matches pflag
// StringSlice CSV semantics; space-separated values still work.
func configUpstreams() []string {
	return normalizeCSVList(viper.GetStringSlice("upstream"))
}

// normalizeCSVList flattens a string list by splitting each entry on commas and
// trimming space. Empty segments are dropped.
func normalizeCSVList(parts []string) []string {
	if len(parts) == 0 {
		return nil
	}
	var out []string
	for _, p := range parts {
		for _, item := range strings.Split(p, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func mustBindEnv(key, env string) {
	if err := viper.BindEnv(key, env); err != nil {
		panic(fmt.Sprintf("failed to bind env %q: %v", env, err))
	}
}

func mustBindPFlag(key string, flag *pflag.Flag) {
	if err := viper.BindPFlag(key, flag); err != nil {
		panic(fmt.Sprintf("failed to bind flag %q: %v", key, err))
	}
}
