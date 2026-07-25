package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fetchurl/fetchurl"
	"github.com/fetchurl/fetchurl/internal/errutil"
	"github.com/fetchurl/fetchurl/internal/httpclient"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:   "get <algo> <hash>",
	Short: "Fetch a file using CAS",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		algo := args[0]
		hash := args[1]
		urls, err := cmd.Flags().GetStringSlice("url")
		if err != nil {
			errutil.ReportError(err, "Failed to get url flag")
			os.Exit(1)
		}
		output, err := cmd.Flags().GetString("output")
		if err != nil {
			errutil.ReportError(err, "Failed to get output flag")
			os.Exit(1)
		}

		f := fetchurl.NewFetcher(httpclient.New())

		var out io.Writer
		var finish func(fetchErr error) error
		if output != "" {
			w, fin, err := openAtomicOutput(output)
			if err != nil {
				errutil.ReportError(err, "Failed to create output temp file", "path", output)
				os.Exit(1)
			}
			out = w
			finish = fin
		} else {
			out = os.Stdout
			finish = func(fetchErr error) error { return fetchErr }
		}

		bar := progressbar.NewOptions64(
			-1,
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionSetDescription("downloading"),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetWidth(10),
			progressbar.OptionThrottle(65*time.Millisecond),
			progressbar.OptionOnCompletion(func() {
				if _, err := fmt.Fprint(os.Stderr, "\n"); err != nil {
					errutil.LogMsg(err, "Failed to print newline to stderr")
				}
			}),
		)

		fetchErr := f.Fetch(cmd.Context(), fetchurl.FetchOptions{
			Algo: algo,
			Hash: hash,
			URLs: urls,
			Out:  io.MultiWriter(out, bar),
		})
		if err := finish(fetchErr); err != nil {
			if fetchErr != nil {
				errutil.ReportError(fetchErr, "Fetch failed")
			} else {
				errutil.ReportError(err, "Failed to finalize output file", "path", output)
			}
			os.Exit(1)
		}
	},
}

// openAtomicOutput creates a temp file in the destination directory and returns
// a writer plus a finish callback. The final path is not truncated or replaced
// until finish(nil) renames the temp into place after a verified fetch.
//
// finish(fetchErr):
//   - if fetchErr != nil: close+remove the temp (leave any existing final path
//     untouched) and return fetchErr
//   - if fetchErr == nil: close the temp, rename onto path, surface close/rename
//     errors (and remove the temp if rename cannot proceed)
func openAtomicOutput(path string) (io.Writer, func(error) error, error) {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".fetchurl-get-*")
	if err != nil {
		return nil, nil, err
	}
	tmpName := tmp.Name()

	finish := func(fetchErr error) error {
		if fetchErr != nil {
			closeErr := tmp.Close()
			if closeErr != nil {
				errutil.LogMsg(closeErr, "Failed to close output temp after failed fetch", "path", tmpName)
			}
			if remErr := os.Remove(tmpName); remErr != nil && !os.IsNotExist(remErr) {
				errutil.LogMsg(remErr, "Failed to remove output temp after failed fetch", "path", tmpName)
			}
			return fetchErr
		}

		if err := tmp.Close(); err != nil {
			if remErr := os.Remove(tmpName); remErr != nil && !os.IsNotExist(remErr) {
				errutil.LogMsg(remErr, "Failed to remove output temp after close error", "path", tmpName)
			}
			return fmt.Errorf("close output temp: %w", err)
		}

		if err := os.Rename(tmpName, path); err != nil {
			if remErr := os.Remove(tmpName); remErr != nil && !os.IsNotExist(remErr) {
				errutil.LogMsg(remErr, "Failed to remove output temp after rename error", "path", tmpName)
			}
			return fmt.Errorf("install output file: %w", err)
		}
		return nil
	}

	return tmp, finish, nil
}

func init() {
	rootCmd.AddCommand(getCmd)
	getCmd.Flags().StringSlice("url", []string{}, "Source URLs")
	getCmd.Flags().StringP("output", "o", "", "Output file")
}
