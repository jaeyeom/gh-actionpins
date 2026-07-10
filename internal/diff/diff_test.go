package diff

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
	"github.com/jaeyeom/gh-actionpins/internal/scan"
)

const catalogYAML = `
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
  require_comment: false
`

func mustCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Parse([]byte(catalogYAML))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func layoutRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCompareStatuses(t *testing.T) {
	t.Parallel()
	// One workflow covering all four statuses.
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
      - uses: actions/setup-go@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      - uses: actions/checkout@v4
      - uses: totally/unknown@v1
`,
	})
	scanRes, err := scan.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compare(mustCatalog(t), scanRes, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 4 {
		t.Fatalf("entries = %d, want 4; %#v", len(res.Entries), res.Entries)
	}

	byActionStatus := map[string]Status{}
	for _, e := range res.Entries {
		key := e.Action + "@" + e.Ref
		byActionStatus[key] = e.Status
	}

	assertStatus := func(action, ref string, want Status) {
		t.Helper()
		key := action + "@" + ref
		if got := byActionStatus[key]; got != want {
			t.Errorf("%s status = %q, want %q", key, got, want)
		}
	}
	assertStatus("actions/checkout", "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", StatusOK)
	assertStatus("actions/setup-go", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", StatusMismatch)
	assertStatus("actions/checkout", "v4", StatusUnpinned)
	assertStatus("totally/unknown", "v1", StatusUnknown)

	if !res.HasDrift() {
		t.Error("HasDrift() = false, want true")
	}
	if res.Summary.OK != 1 || res.Summary.Mismatch != 1 || res.Summary.Unpinned != 1 || res.Summary.Unknown != 1 {
		t.Errorf("summary = %+v", res.Summary)
	}
}

func TestCompareOKCaseInsensitiveSHA(t *testing.T) {
	t.Parallel()
	// Catalog SHA uppercased (40 hex chars).
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  j:
    steps:
      - uses: actions/checkout@9C091BB21B7C1C1D1991BB908D89E4E9DDDFE3E0
`,
	})
	scanRes, err := scan.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compare(mustCatalog(t), scanRes, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Status != StatusOK {
		t.Fatalf("got %#v", res.Entries)
	}
}

func TestCompareRequireComment(t *testing.T) {
	t.Parallel()
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ok.yml": `
jobs:
  j:
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
`,
		".github/workflows/missing.yml": `
jobs:
  j:
    steps:
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16
`,
		".github/workflows/wrong.yml": `
jobs:
  j:
    steps:
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v1.0.0
`,
	})
	scanRes, err := scan.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	enforce := true
	res, err := Compare(mustCatalog(t), scanRes, Options{EnforceComment: &enforce})
	if err != nil {
		t.Fatal(err)
	}

	byFile := map[string]Entry{}
	for _, e := range res.Entries {
		byFile[e.File] = e
	}
	if e := byFile[".github/workflows/ok.yml"]; e.Status != StatusOK {
		t.Errorf("ok.yml status = %q detail=%q", e.Status, e.Detail)
	}
	if e := byFile[".github/workflows/missing.yml"]; e.Status != StatusMismatch || !strings.Contains(e.Detail, "missing version comment") {
		t.Errorf("missing.yml = %q %q", e.Status, e.Detail)
	}
	if e := byFile[".github/workflows/wrong.yml"]; e.Status != StatusMismatch || !strings.Contains(e.Detail, "does not match") {
		t.Errorf("wrong.yml = %q %q", e.Status, e.Detail)
	}
}

func TestCompareEmptyScan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scanRes, err := scan.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compare(mustCatalog(t), scanRes, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.HasDrift() || len(res.Entries) != 0 {
		t.Fatalf("want clean empty, got %#v", res)
	}
}

func TestCompareNilArgs(t *testing.T) {
	t.Parallel()
	if _, err := Compare(nil, &scan.Result{}, Options{}); err == nil {
		t.Error("want error for nil catalog")
	}
	if _, err := Compare(mustCatalog(t), nil, Options{}); err == nil {
		t.Error("want error for nil scan")
	}
}

func TestCompareUnknownNotUnpinned(t *testing.T) {
	t.Parallel()
	// Floating tag for an uncatalogued action is unknown, not unpinned.
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  j:
    steps:
      - uses: someone/else@main
`,
	})
	scanRes, err := scan.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compare(mustCatalog(t), scanRes, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Status != StatusUnknown {
		t.Fatalf("got %#v", res.Entries)
	}
}

func TestWriteTableAndJSON(t *testing.T) {
	t.Parallel()
	res := &Result{
		Root: "/tmp/repo",
		Entries: []Entry{
			{
				File: ".github/workflows/ci.yml", Line: 10, Action: "actions/checkout",
				Ref: "v4", Uses: "actions/checkout@v4", Status: StatusUnpinned,
				CatalogSHA: "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				Detail:     "ref is not a full commit SHA",
			},
		},
		Summary: Summary{Unpinned: 1, Drift: true},
	}

	var table bytes.Buffer
	if err := Write(&table, res, FormatTable); err != nil {
		t.Fatal(err)
	}
	out := table.String()
	if !strings.Contains(out, "STATUS") || !strings.Contains(out, "unpinned") {
		t.Errorf("table unexpected: %q", out)
	}
	if !strings.Contains(out, "summary: drift") {
		t.Errorf("want summary line: %q", out)
	}

	var js bytes.Buffer
	if err := Write(&js, res, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatalf("json: %v\n%s", err, js.String())
	}
	if len(decoded.Entries) != 1 || decoded.Summary.Unpinned != 1 {
		t.Errorf("decoded = %#v", decoded)
	}

	if err := Write(ioDiscard{}, res, "xml"); err == nil {
		t.Error("want error for unknown format")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestWriteEmptyTable(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Write(&buf, &Result{Entries: nil, Summary: Summary{}}, FormatTable); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No action references") {
		t.Errorf("got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "summary: clean") {
		t.Errorf("got %q", buf.String())
	}
}

func TestParseTrailingComment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line string
		want string
	}{
		{`      - uses: actions/checkout@abc # v7.0.0`, "v7.0.0"},
		{`      - uses: actions/checkout@abc`, ""},
		{`uses: x@y #v1`, "v1"},
	}
	for _, tc := range tests {
		if got := parseTrailingComment(tc.line); got != tc.want {
			t.Errorf("parseTrailingComment(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}
