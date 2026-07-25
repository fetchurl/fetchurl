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

	const name = "test-context-probe"
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			rootCmd.RemoveCommand(c)
		}
	}

	var childCtx context.Context
	probe := &cobra.Command{
		Use: name,
		Run: func(cmd *cobra.Command, args []string) {
			childCtx = cmd.Context()
		},
	}
	rootCmd.AddCommand(probe)
	t.Cleanup(func() {
		rootCmd.RemoveCommand(probe)
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	rootCmd.SetArgs([]string{name})
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

	const name = "test-context-already-done"
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			rootCmd.RemoveCommand(c)
		}
	}

	var saw error
	probe := &cobra.Command{
		Use: name,
		RunE: func(cmd *cobra.Command, args []string) error {
			saw = cmd.Context().Err()
			return nil
		},
	}
	rootCmd.AddCommand(probe)
	t.Cleanup(func() {
		rootCmd.RemoveCommand(probe)
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	rootCmd.SetArgs([]string{name})
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(io.Discard)

	if err := execute(ctx); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if saw == nil {
		t.Fatal("expected command context to already be canceled")
	}
}
