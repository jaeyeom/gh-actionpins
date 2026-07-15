package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateActionPinWritesAndPreserves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	// Include comments and multiple actions so we can assert preservation.
	src := `# header comment
actions:
  actions/checkout:
    version: v7.0.0
    sha: 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
    approved_at: 2026-06-01
  actions/setup-go:
    version: v6.5.0
    sha: 924ae3a1cded613372ab5595356fb5720e22ba16
    approved_at: 2026-06-01
policy:
  # soak
  min_age: 7d
  prefer: major
  require_comment: true
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	newSHA := "ffffffffffffffffffffffffffffffffffffffff"
	if err := UpdateActionPin(path, "actions/checkout", Action{
		Version:    "v7.1.0",
		SHA:        newSHA,
		ApprovedAt: "2026-07-15",
	}); err != nil {
		t.Fatalf("UpdateActionPin: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, needle := range []string{
		"# header comment",
		"# soak",
		"version: v7.1.0",
		"sha: " + newSHA,
		"approved_at: 2026-07-15",
		"actions/setup-go:",
		"v6.5.0",
	} {
		if !strings.Contains(out, needle) {
			t.Errorf("output missing %q:\n%s", needle, out)
		}
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load after update: %v", err)
	}
	got := c.Actions["actions/checkout"]
	if got.Version != "v7.1.0" || got.SHA != newSHA || got.ApprovedAt != "2026-07-15" {
		t.Errorf("checkout pin = %+v", got)
	}
	if c.Actions["actions/setup-go"].Version != "v6.5.0" {
		t.Errorf("setup-go changed: %+v", c.Actions["actions/setup-go"])
	}
}

func TestUpdateActionPinUnknownAction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdateActionPin(path, "not/in-catalog", Action{
		Version:    "v1.0.0",
		SHA:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ApprovedAt: "2026-07-15",
	})
	if err == nil || !strings.Contains(err.Error(), "not in catalog") {
		t.Fatalf("error = %v, want not in catalog", err)
	}
}

func TestUpdateActionPinRejectsBadSHA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdateActionPin(path, "actions/checkout", Action{
		Version:    "v7.1.0",
		SHA:        "short",
		ApprovedAt: "2026-07-15",
	})
	if err == nil || !strings.Contains(err.Error(), "sha") {
		t.Fatalf("error = %v, want sha validation", err)
	}
	// File must be unchanged.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "v7.1.0") {
		t.Error("file was written despite validation failure")
	}
}

func TestUpdateActionPinMissingFile(t *testing.T) {
	t.Parallel()
	err := UpdateActionPin(filepath.Join(t.TempDir(), "missing.yaml"), "actions/checkout", Action{
		Version:    "v1",
		SHA:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ApprovedAt: "2026-01-01",
	})
	if err == nil || !strings.Contains(err.Error(), "read catalog") {
		t.Fatalf("error = %v, want read catalog", err)
	}
}
