package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAtomicOutputSuccessReplacesFinal(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "artifact.bin")
	if err := os.WriteFile(final, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, finish, err := openAtomicOutput(final)
	if err != nil {
		t.Fatalf("openAtomicOutput: %v", err)
	}

	// Final path must stay intact until a successful finish.
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("read final mid-write: %v", err)
	}
	if string(got) != "stale" {
		t.Fatalf("final mid-write = %q, want stale (not truncated)", got)
	}

	if _, err := w.Write([]byte("verified")); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := finish(nil); err != nil {
		t.Fatalf("finish: %v", err)
	}

	got, err = os.ReadFile(final)
	if err != nil {
		t.Fatalf("read final after finish: %v", err)
	}
	if string(got) != "verified" {
		t.Fatalf("final = %q, want verified", got)
	}

	// No leftover temps in the destination dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "artifact.bin" {
			t.Errorf("unexpected leftover entry %q", e.Name())
		}
	}
}

func TestOpenAtomicOutputFetchErrorLeavesFinalUntouched(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "artifact.bin")
	if err := os.WriteFile(final, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, finish, err := openAtomicOutput(final)
	if err != nil {
		t.Fatalf("openAtomicOutput: %v", err)
	}
	if _, err := w.Write([]byte("partial-bad")); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	fetchErr := errors.New("hash mismatch")
	if err := finish(fetchErr); !errors.Is(err, fetchErr) {
		t.Fatalf("finish = %v, want fetchErr", err)
	}

	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	if string(got) != "keep-me" {
		t.Fatalf("final = %q, want keep-me", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "artifact.bin" {
			t.Errorf("temp should be removed, found %q", e.Name())
		}
	}
}

func TestOpenAtomicOutputFetchErrorNoPriorFinal(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "new-artifact.bin")

	w, finish, err := openAtomicOutput(final)
	if err != nil {
		t.Fatalf("openAtomicOutput: %v", err)
	}
	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := finish(errors.New("network failed")); err == nil {
		t.Fatal("expected finish to return fetch error")
	}

	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("final path should not exist after failed fetch, stat err=%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty dir after abort, got %v", entries)
	}
}
