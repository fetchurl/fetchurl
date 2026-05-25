package main

import "testing"

func TestSeedCommandRequiresStoreAndURLList(t *testing.T) {
	if err := seedCmd.Args(seedCmd, []string{"/tmp/cache"}); err == nil {
		t.Fatal("expected positional args validation to fail")
	}

	if err := seedCmd.Args(seedCmd, []string{"/tmp/cache", "/tmp/urls.txt"}); err != nil {
		t.Fatalf("expected positional args validation to pass: %v", err)
	}
}
