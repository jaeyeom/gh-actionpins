package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("run(%v) = %d, want 0", args, code)
		}
		if !strings.Contains(stdout.String(), "gh-actionpins") {
			t.Errorf("run(%v) stdout missing usage", args)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"not-a-command"}, &stdout, &stderr); code != 1 {
		t.Errorf("run([not-a-command]) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr = %q, want unknown command", stderr.String())
	}
}

func TestRunCatalogValidate(t *testing.T) {
	t.Parallel()
	// examples/catalog.yaml relative to module root when tests run from package dir.
	example := filepath.Join("..", "..", "examples", "catalog.yaml")
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("example catalog missing: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"catalog", "validate", "--catalog", example}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("catalog validate = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "catalog OK") {
		t.Errorf("stdout = %q, want catalog OK", stdout.String())
	}
	if !strings.Contains(stdout.String(), "3 actions") {
		t.Errorf("stdout = %q, want 3 actions", stdout.String())
	}
}

func TestRunCatalogValidateInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("actions:\n  x:\n    version: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"catalog", "validate", "--catalog", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "sha") {
		t.Errorf("stderr = %q, want sha error", stderr.String())
	}
}

func TestRunCatalogMissingSubcommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"catalog"}, &stdout, &stderr); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}

func TestRunCatalogUnknownSubcommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"catalog", "nope"}, &stdout, &stderr); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}
