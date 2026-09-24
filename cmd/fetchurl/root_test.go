package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/spf13/cobra"
)

// execute must use ExecuteContext so cancel/deadline from the process root
// (SIGINT via NotifyContext in Execute) reaches get/seed/server via cmd.Context().
func TestExecutePropagatesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var childCtx context.Context
	probe := &cobra.Command{
		Use: "test-context-probe",
		Run: func(cmd *cobra.Command, args []string) {
			childCtx = cmd.Context()
		},
	}
	withRootProbe(t, probe)

	rootCmd.SetArgs([]string{probe.Name()})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	if err := execute(ctx); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if childCtx == nil {
		t.Fatal("probe did not observe command context")
	}

	cancel()
	select {
	case <-childCtx.Done():
		// parent cancel must reach the command context
	default:
		t.Fatal("command context was not derived from execute's context")
	}
}

func TestExecuteCanceledContextStillInvokesCommand(t *testing.T) {
	// Pre-canceled context must still be visible on the command (callers
	// check cmd.Context().Err() themselves; cobra does not skip Run).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var saw error
	probe := &cobra.Command{
		Use: "test-context-already-done",
		RunE: func(cmd *cobra.Command, args []string) error {
			saw = cmd.Context().Err()
			return nil
		},
	}
	withRootProbe(t, probe)

	rootCmd.SetArgs([]string{probe.Name()})
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(io.Discard)

	if err := execute(ctx); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if saw == nil {
		t.Fatal("expected command context to already be canceled")
	}
}

// withRootProbe installs probe on the package root command and removes it
// after the test, including args and output streams left on rootCmd.
func withRootProbe(t *testing.T, probe *cobra.Command) {
	t.Helper()
	for _, c := range rootCmd.Commands() {
		if c.Name() == probe.Name() {
			rootCmd.RemoveCommand(c)
		}
	}
	rootCmd.AddCommand(probe)
	t.Cleanup(func() {
		rootCmd.RemoveCommand(probe)
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
}
