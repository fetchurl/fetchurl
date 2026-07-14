package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/fetchurl/fetchurl"
	"github.com/fetchurl/fetchurl/internal/errutil"
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

		// Do not use http.DefaultClient (no dial/header bounds). Do not set
		// Client.Timeout either — that covers the whole transfer and would
		// abort multi-GB downloads on slow links. Bound dial + TLS + response
		// headers only; body streaming may run as long as the peer sends data.
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		}

		f := fetchurl.NewFetcher(client)

		var out io.Writer
		if output != "" {
			file, err := os.Create(output)
			if err != nil {
				errutil.ReportError(err, "Failed to create output file")
				os.Exit(1)
			}
			defer func() {
				errutil.LogMsg(file.Close(), "Failed to close output file")
			}()
			out = file
		} else {
			out = os.Stdout
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

		if err := f.Fetch(cmd.Context(), fetchurl.FetchOptions{
			Algo: algo,
			Hash: hash,
			URLs: urls,
			Out:  io.MultiWriter(out, bar),
		}); err != nil {
			errutil.ReportError(err, "Fetch failed")
			if output != "" {
				errutil.LogMsg(os.Remove(output), "Failed to remove output file after failed fetch", "path", output)
			}
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(getCmd)
	getCmd.Flags().StringSlice("url", []string{}, "Source URLs")
	getCmd.Flags().StringP("output", "o", "", "Output file")
}
