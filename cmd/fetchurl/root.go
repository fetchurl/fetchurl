package main

import (
	"fmt"
	"os"

	"github.com/lucasew/fetchurl/internal/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "fetchurl",
	Short: "A Content-Addressable Storage (CAS) proxy",
	Long:  `fetchurl is a CLI tool that implements a Content-Addressable Storage (CAS) proxy.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if _, printErr := fmt.Fprintln(os.Stderr, err); printErr != nil {
			util.ReportError(printErr, "Failed to print error to stderr")
		}
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	viper.SetEnvPrefix("FETCHURL")
	viper.AutomaticEnv()
}
