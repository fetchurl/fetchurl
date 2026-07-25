package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fetchurl/fetchurl/internal/errutil"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "fetchurl",
	Short: "A Content-Addressable Storage (CAS) proxy",
	Long:  `fetchurl is a CLI tool that implements a Content-Addressable Storage (CAS) proxy.`,
}

// Execute runs the CLI. SIGINT/SIGTERM cancel the root context so subcommands
// that honor cmd.Context() (get, seed, server) abort in-flight work and run
// defers instead of relying on an uncatchable process kill.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx); err != nil {
		if _, printErr := fmt.Fprintln(os.Stderr, err); printErr != nil {
			errutil.ReportError(printErr, "Failed to print error to stderr")
		}
		os.Exit(1)
	}
}

func execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	viper.SetEnvPrefix("FETCHURL")
	viper.AutomaticEnv()
}
