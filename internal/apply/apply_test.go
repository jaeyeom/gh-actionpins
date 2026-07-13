package apply

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
	"github.com/jaeyeom/gh-actionpins/internal/diff"
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
  golangci/golangci-lint-action:
    version: v9.3.0
    sha: ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a
    approved_at: 2026-06-01
policy:
  require_comment: true
`

const catalogNoCommentYAML = `
actions:
  actions/checkout:
    version: v7.0.0
    sha: 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
policy:
  require_comment: false
`

func mustCatalog(t *testing.T, yaml string) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Parse([]byte(yaml))
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

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunApplyRewritesMismatchedAndUnpinned(t *testing.T) {
	t.Parallel()
	before := `name: CI

on: push

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa # v1.0.0
        with:
          go-version: "1.22"
      - name: local composite
        uses: ./actions/local-build
      - name: docker step
        uses: docker://alpine:3.19
      - uses: not/in-catalog@v1
      - uses: golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0
`
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": before,
	})

	res, err := Run(mustCatalog(t, catalogYAML), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.DryRun {
		t.Error("DryRun = true, want false")
	}
	if len(res.Changes) != 2 {
		t.Fatalf("changes = %d, want 2; %+v", len(res.Changes), res.Changes)
	}

	after := readFile(t, dir, ".github/workflows/ci.yml")
	mustContain(t, after, []string{
		"actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0",
		"actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0",
		"golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0",
		"uses: not/in-catalog@v1",
		"uses: ./actions/local-build",
		"uses: docker://alpine:3.19",
		`go-version: "1.22"`,
		"name: local composite",
	})
	assertSkippedStatuses(t, res.Skipped, diff.StatusOK, diff.StatusUnknown)
}

func mustContain(t *testing.T, haystack string, needles []string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("missing %q in:\n%s", n, haystack)
		}
	}
}

func assertSkippedStatuses(t *testing.T, skips []Skip, want ...diff.Status) {
	t.Helper()
	seen := map[diff.Status]bool{}
	for _, s := range skips {
		seen[s.Status] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("skipped missing status %q; got %+v", w, skips)
		}
	}
}

func TestRunDryRunDoesNotWrite(t *testing.T) {
	t.Parallel()
	before := `
jobs:
  j:
    steps:
      - uses: actions/checkout@v4
`
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": before,
	})

	res, err := Run(mustCatalog(t, catalogYAML), dir, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun {
		t.Error("DryRun = false, want true")
	}
	if len(res.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(res.Changes))
	}
	if res.Changes[0].NewUses != "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0" {
		t.Errorf("NewUses = %q", res.Changes[0].NewUses)
	}

	after := readFile(t, dir, ".github/workflows/ci.yml")
	if after != before {
		t.Errorf("dry-run modified file:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestRunNoCommentPolicy(t *testing.T) {
	t.Parallel()
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  j:
    steps:
      - uses: actions/checkout@v4 # keep-me-gone
`,
	})
	res, err := Run(mustCatalog(t, catalogNoCommentYAML), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(res.Changes))
	}
	if res.Changes[0].NewUses != "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0" {
		t.Errorf("NewUses = %q, want SHA without comment", res.Changes[0].NewUses)
	}
	after := readFile(t, dir, ".github/workflows/ci.yml")
	if strings.Contains(after, "keep-me-gone") {
		t.Errorf("old comment should be replaced:\n%s", after)
	}
	if strings.Contains(after, "# v7.0.0") {
		t.Errorf("should not add version comment when require_comment false:\n%s", after)
	}
}

func TestRunIdempotentWhenAlreadyPinned(t *testing.T) {
	t.Parallel()
	content := `
jobs:
  j:
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
`
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": content,
	})
	res, err := Run(mustCatalog(t, catalogYAML), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("changes = %d, want 0; %+v", len(res.Changes), res.Changes)
	}
	after := readFile(t, dir, ".github/workflows/ci.yml")
	if after != content {
		t.Errorf("file changed when already ok")
	}
}

func TestRunPreservesIndentAndListMarker(t *testing.T) {
	t.Parallel()
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - uses: actions/checkout@v4\n",
	})
	_, err := Run(mustCatalog(t, catalogYAML), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	after := readFile(t, dir, ".github/workflows/ci.yml")
	want := "      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0"
	if !strings.Contains(after, want) {
		t.Errorf("after=\n%s\nwant line %q", after, want)
	}
}

func TestRunQuotedUses(t *testing.T) {
	t.Parallel()
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  j:
    steps:
      - uses: "actions/checkout@v4"
`,
	})
	res, err := Run(mustCatalog(t, catalogYAML), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("changes = %d, want 1; %+v", len(res.Changes), res.Changes)
	}
	after := readFile(t, dir, ".github/workflows/ci.yml")
	// Apply writes unquoted trusted form (standard pin style).
	if !strings.Contains(after, "uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0") {
		t.Errorf("quoted uses not rewritten:\n%s", after)
	}
}

func TestRunMultipleFiles(t *testing.T) {
	t.Parallel()
	dir := layoutRepo(t, map[string]string{
		".github/workflows/a.yml": `
jobs:
  j:
    steps:
      - uses: actions/checkout@v4
`,
		".github/workflows/b.yml": `
jobs:
  j:
    steps:
      - uses: actions/setup-go@v5
`,
	})
	res, err := Run(mustCatalog(t, catalogYAML), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 {
		t.Fatalf("changes = %d, want 2", len(res.Changes))
	}
	a := readFile(t, dir, ".github/workflows/a.yml")
	b := readFile(t, dir, ".github/workflows/b.yml")
	if !strings.Contains(a, "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0") {
		t.Errorf("a.yml not updated:\n%s", a)
	}
	if !strings.Contains(b, "924ae3a1cded613372ab5595356fb5720e22ba16") {
		t.Errorf("b.yml not updated:\n%s", b)
	}
}

func TestWriteTableAndJSON(t *testing.T) {
	t.Parallel()
	dir := layoutRepo(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  j:
    steps:
      - uses: actions/checkout@v4
      - uses: unknown/action@v1
`,
	})
	res, err := Run(mustCatalog(t, catalogYAML), dir, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Write(&buf, res, FormatTable); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "FILE") || !strings.Contains(out, "actions/checkout") {
		t.Errorf("table = %q", out)
	}
	if !strings.Contains(out, "would apply 1 change") {
		t.Errorf("table summary = %q", out)
	}

	buf.Reset()
	if err := Write(&buf, res, FormatJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"dryRun": true`) {
		t.Errorf("json = %q", buf.String())
	}
}

func TestRewriteUsesLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		line    string
		old     string
		new     string
		want    string
		wantErr bool
	}{
		{
			name: "simple",
			line: "      - uses: actions/checkout@v4",
			old:  "actions/checkout@v4",
			new:  "actions/checkout@abc # v1",
			want: "      - uses: actions/checkout@abc # v1",
		},
		{
			name: "with comment",
			line: "      - uses: actions/checkout@deadbeef # old",
			old:  "actions/checkout@deadbeef",
			new:  "actions/checkout@abc # v1",
			want: "      - uses: actions/checkout@abc # v1",
		},
		{
			name: "mapping style",
			line: "        uses: actions/setup-go@v5",
			old:  "actions/setup-go@v5",
			new:  "actions/setup-go@sha # v6",
			want: "        uses: actions/setup-go@sha # v6",
		},
		{
			name:    "mismatch old",
			line:    "      - uses: actions/checkout@v4",
			old:     "actions/checkout@v5",
			new:     "x",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := rewriteUsesLine(tc.line, tc.old, tc.new)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunNilCatalog(t *testing.T) {
	t.Parallel()
	_, err := Run(nil, t.TempDir(), Options{})
	if err == nil {
		t.Fatal("expected error")
	}
}
