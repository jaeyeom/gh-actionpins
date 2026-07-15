package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
)

func writeTestCatalog(t *testing.T, prefer string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	content := `actions:
  actions/checkout:
    version: v4.0.0
    sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    approved_at: 2026-01-01
  actions/setup-go:
    version: v5.0.0
    sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    approved_at: 2026-01-01
policy:
  min_age: 7d
  prefer: ` + prefer + `
  require_comment: true
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApproveBumpExplicitWrite(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	newSHA := "1234567890abcdef1234567890abcdef12345678"
	a, err := ApproveBump(context.Background(), cat, "checkout", ApproveOptions{
		CatalogPath: path,
		Version:     "v4.2.0",
		SHA:         newSHA,
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ApproveBump: %v", err)
	}
	if a.Action != "actions/checkout" {
		t.Errorf("Action = %q", a.Action)
	}
	if a.ToVersion != "v4.2.0" || a.ToSHA != newSHA {
		t.Errorf("to = %s %s", a.ToVersion, a.ToSHA)
	}
	if a.FromVersion != "v4.0.0" {
		t.Errorf("FromVersion = %q", a.FromVersion)
	}
	if a.ApprovedAt != "2026-07-15" {
		t.Errorf("ApprovedAt = %q", a.ApprovedAt)
	}
	if a.Source != "explicit" {
		t.Errorf("Source = %q", a.Source)
	}
	if a.MajorJump {
		t.Error("MajorJump = true, want false")
	}

	reloaded, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pin := reloaded.Actions["actions/checkout"]
	if pin.Version != "v4.2.0" || pin.SHA != newSHA || pin.ApprovedAt != "2026-07-15" {
		t.Errorf("reloaded pin = %+v", pin)
	}
	// Other action untouched.
	if reloaded.Actions["actions/setup-go"].Version != "v5.0.0" {
		t.Errorf("setup-go changed: %+v", reloaded.Actions["actions/setup-go"])
	}
}

func TestApproveBumpFromProposal(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	old := now.Add(-10 * 24 * time.Hour)
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "v4.1.0", PublishedAt: old},
			},
		},
		shas: map[string]string{
			"actions/checkout@v4.1.0": "cccccccccccccccccccccccccccccccccccccccc",
		},
	}
	a, err := ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		CatalogPath: path,
		Lookup:      lookup,
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ApproveBump: %v", err)
	}
	if a.Source != "proposal" {
		t.Errorf("Source = %q", a.Source)
	}
	if a.ToVersion != "v4.1.0" || a.ToSHA != "cccccccccccccccccccccccccccccccccccccccc" {
		t.Errorf("to = %s %s", a.ToVersion, a.ToSHA)
	}
	reloaded, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Actions["actions/checkout"].Version != "v4.1.0" {
		t.Errorf("catalog not updated: %+v", reloaded.Actions["actions/checkout"])
	}
}

func TestApproveBumpMajorRefusedWithoutAllowMajor(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferSameMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	_, err = ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		CatalogPath: path,
		Version:     "v5.0.0",
		SHA:         "dddddddddddddddddddddddddddddddddddddddd",
		Now:         now,
	})
	if err == nil {
		t.Fatal("expected major jump refusal")
	}
	if !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "major jump") {
		t.Errorf("error = %v, want major jump refused", err)
	}
	if !strings.Contains(err.Error(), "--allow-major") {
		t.Errorf("error = %v, want --allow-major hint", err)
	}
	// Catalog unchanged.
	reloaded, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Actions["actions/checkout"].Version != "v4.0.0" {
		t.Errorf("catalog mutated on refusal: %+v", reloaded.Actions["actions/checkout"])
	}
}

func TestApproveBumpMajorAllowedWithFlag(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferSameMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	a, err := ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		CatalogPath: path,
		Version:     "v5.0.0",
		SHA:         "dddddddddddddddddddddddddddddddddddddddd",
		AllowMajor:  true,
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ApproveBump: %v", err)
	}
	if !a.MajorJump {
		t.Error("MajorJump = false, want true")
	}
	if a.ToVersion != "v5.0.0" {
		t.Errorf("ToVersion = %q", a.ToVersion)
	}
}

func TestApproveBumpMajorOKWhenPreferMajor(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	a, err := ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		CatalogPath: path,
		Version:     "v5.0.0",
		SHA:         "dddddddddddddddddddddddddddddddddddddddd",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ApproveBump: %v", err)
	}
	if !a.MajorJump {
		t.Error("MajorJump = false, want true")
	}
}

func TestApproveBumpProposalTooNew(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "v4.9.0", PublishedAt: now}, // day 0
			},
		},
		shas: map[string]string{
			"actions/checkout@v4.9.0": "9999999999999999999999999999999999999999",
		},
	}
	_, err = ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		CatalogPath: path,
		Lookup:      lookup,
		Now:         now,
	})
	if err == nil {
		t.Fatal("expected refuse for day-0 proposal path")
	}
}

func TestApproveBumpRequiresVersionAndSHATogether(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		CatalogPath: path,
		Version:     "v4.1.0",
		// SHA missing
	})
	if err == nil || !strings.Contains(err.Error(), "--version and --sha") {
		t.Fatalf("error = %v, want version/sha together", err)
	}
}

func TestApproveBumpBadSHA(t *testing.T) {
	t.Parallel()
	path := writeTestCatalog(t, catalog.PreferMajor)
	cat, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		CatalogPath: path,
		Version:     "v4.1.0",
		SHA:         "notasha",
	})
	if err == nil || !strings.Contains(err.Error(), "sha") {
		t.Fatalf("error = %v, want sha error", err)
	}
}

func TestApproveBumpRequiresCatalogPath(t *testing.T) {
	t.Parallel()
	cat := testCatalog("7d", catalog.PreferMajor)
	_, err := ApproveBump(context.Background(), cat, "actions/checkout", ApproveOptions{
		Version: "v4.1.0",
		SHA:     "1234567890abcdef1234567890abcdef12345678",
	})
	if err == nil || !strings.Contains(err.Error(), "catalog path") {
		t.Fatalf("error = %v, want catalog path", err)
	}
}

func TestWriteApproval(t *testing.T) {
	t.Parallel()
	a := &Approval{
		CatalogPath: "cat.yaml",
		Action:      "actions/checkout",
		FromVersion: "v4.0.0",
		FromSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ToVersion:   "v4.1.0",
		ToSHA:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ApprovedAt:  "2026-07-15",
		Prefer:      catalog.PreferMajor,
		Source:      "explicit",
		Note:        "catalog pin updated",
	}
	var buf strings.Builder
	if err := WriteApproval(&buf, a, FormatTable); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "approved bump") || !strings.Contains(out, "v4.1.0") {
		t.Errorf("text = %q", out)
	}
	buf.Reset()
	if err := WriteApproval(&buf, a, FormatJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"action": "actions/checkout"`) {
		t.Errorf("json = %q", buf.String())
	}
}
