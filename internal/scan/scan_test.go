package scan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func layoutWorkflows(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(wf, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func copyTestdataWorkflows(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wfSrc := filepath.Join("testdata", "workflows")
	wfDst := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(wfSrc)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(wfSrc, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wfDst, e.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanTestdata(t *testing.T) {
	t.Parallel()
	dir := copyTestdataWorkflows(t)

	res, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	wantActions := []string{
		"actions/checkout",
		"actions/setup-go",
		"golangci/golangci-lint-action",
		"org/repo/.github/workflows/shared.yml",
		"owner/name/path",
	}
	if len(res.Findings) != len(wantActions) {
		t.Fatalf("len(Findings) = %d, want %d; got %#v", len(res.Findings), len(wantActions), res.Findings)
	}

	gotSet := map[string]Finding{}
	for _, f := range res.Findings {
		gotSet[f.Action] = f
		if strings.HasPrefix(f.Uses, "./") || strings.HasPrefix(f.Uses, "docker://") {
			t.Errorf("local/docker uses should be skipped: %q", f.Uses)
		}
	}
	for _, a := range wantActions {
		if _, ok := gotSet[a]; !ok {
			t.Errorf("missing action %q", a)
		}
	}

	assertRef(t, gotSet, "actions/checkout", "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	assertRef(t, gotSet, "actions/setup-go", "v5")
	assertRef(t, gotSet, "owner/name/path", "abc123def456")
	assertRef(t, gotSet, "org/repo/.github/workflows/shared.yml", "v1")

	assertStableScan(t, dir, res)
	assertFindingPaths(t, res.Findings)
}

func assertRef(t *testing.T, got map[string]Finding, action, wantRef string) {
	t.Helper()
	f, ok := got[action]
	if !ok {
		t.Errorf("missing action %q", action)
		return
	}
	if f.Ref != wantRef {
		t.Errorf("%s ref = %q, want %q", action, f.Ref, wantRef)
	}
}

func assertStableScan(t *testing.T, dir string, first *Result) {
	t.Helper()
	second, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Findings) != len(first.Findings) {
		t.Fatalf("second scan length mismatch")
	}
	for i := range first.Findings {
		if first.Findings[i] != second.Findings[i] {
			t.Errorf("finding[%d] not stable: %#v vs %#v", i, first.Findings[i], second.Findings[i])
		}
	}
}

func assertFindingPaths(t *testing.T, findings []Finding) {
	t.Helper()
	for _, f := range findings {
		if !strings.HasPrefix(f.File, ".github/workflows/") {
			t.Errorf("file = %q, want under .github/workflows/", f.File)
		}
		if strings.Contains(f.File, "\\") {
			t.Errorf("file path should use slashes: %q", f.File)
		}
		if f.Line < 1 {
			t.Errorf("line must be >= 1, got %d for %s", f.Line, f.Action)
		}
	}
}

func TestScanSkipsLocalAndDocker(t *testing.T) {
	t.Parallel()
	dir := layoutWorkflows(t, map[string]string{
		"x.yml": `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: ./local-action
      - uses: ../other-action
      - uses: docker://node:20
      - uses: actions/checkout@v4
`,
	})
	res, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %#v, want only actions/checkout", res.Findings)
	}
	if res.Findings[0].Action != "actions/checkout" {
		t.Errorf("action = %q", res.Findings[0].Action)
	}
}

func TestScanMissingWorkflowsDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan empty repo: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("want empty findings, got %#v", res.Findings)
	}
}

func TestScanIgnoresNonYAML(t *testing.T) {
	t.Parallel()
	dir := layoutWorkflows(t, map[string]string{
		"README.md": "uses: actions/checkout@v4\n",
		"ok.yaml":   "jobs:\n  j:\n    uses: a/b@c\n",
	})
	res, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Action != "a/b" {
		t.Fatalf("findings = %#v", res.Findings)
	}
}

func TestFindingFromUses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		raw    string
		wantOK bool
		action string
		ref    string
	}{
		{name: "sha pin", raw: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", wantOK: true, action: "actions/checkout", ref: "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0"},
		{name: "path action", raw: "owner/name/path@v1", wantOK: true, action: "owner/name/path", ref: "v1"},
		{name: "local", raw: "./foo", wantOK: false},
		{name: "docker", raw: "docker://alpine:3", wantOK: false},
		{name: "empty", raw: "", wantOK: false},
		{name: "no at", raw: "actions/checkout", wantOK: false},
		{name: "no slash", raw: "checkout@v1", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, ok := findingFromUses("f.yml", 1, tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (f=%#v)", ok, tc.wantOK, f)
			}
			if !tc.wantOK {
				return
			}
			if f.Action != tc.action || f.Ref != tc.ref {
				t.Errorf("got action=%q ref=%q, want %q %q", f.Action, f.Ref, tc.action, tc.ref)
			}
		})
	}
}

func TestWriteTableAndJSON(t *testing.T) {
	t.Parallel()
	res := &Result{
		Root: "/tmp/repo",
		Findings: []Finding{
			{File: ".github/workflows/ci.yml", Line: 10, Action: "actions/checkout", Ref: "v4", Uses: "actions/checkout@v4"},
			{File: ".github/workflows/ci.yml", Line: 12, Action: "actions/setup-go", Ref: "v5", Uses: "actions/setup-go@v5"},
		},
	}

	var table bytes.Buffer
	if err := Write(&table, res, FormatTable); err != nil {
		t.Fatal(err)
	}
	out := table.String()
	if !strings.Contains(out, "FILE") || !strings.Contains(out, "actions/checkout") {
		t.Errorf("table output unexpected: %q", out)
	}
	var table2 bytes.Buffer
	if err := Write(&table2, res, FormatTable); err != nil {
		t.Fatal(err)
	}
	if table.String() != table2.String() {
		t.Error("table output not deterministic")
	}

	var js bytes.Buffer
	if err := Write(&js, res, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatalf("json: %v\n%s", err, js.String())
	}
	if len(decoded.Findings) != 2 {
		t.Errorf("json findings = %d", len(decoded.Findings))
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
	if err := Write(&buf, &Result{Findings: nil}, FormatTable); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No action references") {
		t.Errorf("got %q", buf.String())
	}
}

func TestScanInvalidYAML(t *testing.T) {
	t.Parallel()
	dir := layoutWorkflows(t, map[string]string{
		"bad.yml": ":\n  - [",
	})
	if _, err := Scan(dir); err == nil {
		t.Fatal("want parse error")
	}
}
