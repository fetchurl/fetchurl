package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lucasew/fetchurl/internal/app"
	"github.com/lucasew/fetchurl/internal/util"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Starts the HTTP server",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := app.Config{
			Port:             viper.GetInt("port"),
			CacheDir:         viper.GetString("cache-dir"),
			MaxCacheSize:     viper.GetInt64("max-cache-size"),
			MinFreeSpace:     viper.GetInt64("min-free-space"),
			EvictionInterval: viper.GetDuration("eviction-interval"),
			EvictionStrategy: viper.GetString("eviction-strategy"),
			Upstreams:        viper.GetStringSlice("upstream"),
		}

		server, cleanup, err := app.NewServer(cmd.Context(), cfg)
		if err != nil {
			util.ReportError(err, "Failed to initialize server")
			os.Exit(1)
		}
		defer cleanup()

		if err := server.ListenAndServe(); err != nil {
			util.ReportError(err, "Server failed")
			os.Exit(1)
		}
	},
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
	serverCmd.Flags().StringSlice("upstream", []string{}, "Upstream fetchurl servers")

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
