package main

import (
	"fmt"

	"github.com/lucasew/fetchurl/internal/app"
	"github.com/lucasew/fetchurl/internal/errutil"
	"github.com/spf13/cobra"
)

var seedCmd = &cobra.Command{
	Use:   "seed <store> <url-list>",
	Short: "Pre-seed a cache store from a list of URLs",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := app.SeedCacheWithOptions(cmd.Context(), app.SeedOptions{
			CacheDir:    args[0],
			URLListPath: args[1],
			ProgressOut: cmd.ErrOrStderr(),
		})
		if _, printErr := fmt.Fprintf(
			cmd.OutOrStdout(),
			"processed=%d seeded=%d skipped=%d failed=%d\n",
			result.Processed,
			result.Seeded,
			result.Skipped,
			result.Failed,
		); printErr != nil {
			errutil.ReportError(printErr, "Failed to print seed summary")
			return printErr
		}

		if err != nil {
			errutil.ReportError(err, "Failed to seed cache", "store", args[0], "url_list", args[1])
		}

		return err
	},
}

func init() {
	rootCmd.AddCommand(seedCmd)
}
