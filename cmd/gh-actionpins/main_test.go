package main

import (
	"bytes"
	"fmt"
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

func TestRunScan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: ./local
      - uses: docker://alpine:3
`
	if err := os.WriteFile(filepath.Join(wf, "ci.yml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"scan", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("scan = %d, stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "actions/checkout") {
		t.Errorf("stdout missing checkout: %q", out)
	}
	if strings.Contains(out, "./local") || strings.Contains(out, "docker://") {
		t.Errorf("stdout should skip local/docker: %q", out)
	}

	stdout.Reset()
	stderr.Reset()
	// Flags before the optional path (standard flag package).
	code = run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("scan json = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"action": "actions/checkout"`) {
		t.Errorf("json stdout = %q", stdout.String())
	}
}

func TestRunScanTooManyArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "a", "b"}, &stdout, &stderr); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestRunDiff(t *testing.T) {
	t.Parallel()
	example := filepath.Join("..", "..", "examples", "catalog.yaml")
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("example catalog missing: %v", err)
	}

	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	// Match example catalog SHA for checkout (with comment; catalog has require_comment: true).
	content := `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
      - uses: actions/setup-go@v5
      - uses: not/in-catalog@v1
`
	if err := os.WriteFile(filepath.Join(wf, "ci.yml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"diff", "--catalog", example, dir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("diff with drift = %d, want 1; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "ok") || !strings.Contains(out, "unpinned") || !strings.Contains(out, "unknown") {
		t.Errorf("stdout missing statuses: %q", out)
	}
	if !strings.Contains(out, "summary: drift") {
		t.Errorf("stdout missing drift summary: %q", out)
	}

	// Clean repo: only ok pins.
	cleanDir := t.TempDir()
	cleanWF := filepath.Join(cleanDir, ".github", "workflows")
	if err := os.MkdirAll(cleanWF, 0o755); err != nil {
		t.Fatal(err)
	}
	clean := `
jobs:
  j:
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0
      - uses: golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0
`
	if err := os.WriteFile(filepath.Join(cleanWF, "ci.yml"), []byte(clean), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"diff", "--catalog", example, "--format", "json", cleanDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("clean diff = %d, stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"drift": false`) {
		t.Errorf("json want drift false: %q", stdout.String())
	}
}

func TestRunDiffTooManyArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"diff", "a", "b"}, &stdout, &stderr); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestRunDiffMissingCatalog(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"diff", "--catalog", filepath.Join(t.TempDir(), "nope.yaml"), t.TempDir()}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "error:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunApply(t *testing.T) {
	t.Parallel()
	example := filepath.Join("..", "..", "examples", "catalog.yaml")
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("example catalog missing: %v", err)
	}

	dir, path, before := writeApplyFixture(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"apply", "--catalog", example, "--dry-run", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply dry-run = %d, stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "would apply") || !strings.Contains(out, "actions/checkout") {
		t.Errorf("dry-run stdout = %q", out)
	}
	assertFileEquals(t, path, before)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"apply", "--catalog", example, "--format", "json", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply = %d, stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"dryRun": false`) {
		t.Errorf("json stdout = %q", stdout.String())
	}
	assertFileContains(t, path, []string{
		"actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0",
		"actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0",
		"uses: not/in-catalog@v1",
		"uses: ./local",
		"uses: docker://alpine:3",
	})
}

func writeApplyFixture(t *testing.T) (dir, path, before string) {
	t.Helper()
	dir = t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	before = `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - uses: not/in-catalog@v1
      - uses: ./local
      - uses: docker://alpine:3
`
	path = filepath.Join(wf, "ci.yml")
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path, before
}

func assertFileEquals(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("file content mismatch")
	}
}

func assertFileContains(t *testing.T, path string, needles []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after := string(data)
	for _, n := range needles {
		if !strings.Contains(after, n) {
			t.Errorf("missing %q in:\n%s", n, after)
		}
	}
}

func TestRunApplyTooManyArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"apply", "a", "b"}, &stdout, &stderr); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestRunApplyPRAndCommitMutuallyExclusive(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--pr", "--commit"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunApplyCommitDryRunNoGitRepo(t *testing.T) {
	t.Parallel()
	example := filepath.Join("..", "..", "examples", "catalog.yaml")
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("example catalog missing: %v", err)
	}
	dir, _, _ := writeApplyFixture(t)
	// dir is not a git repo — dry-run --commit should fail safely after planning.
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--catalog", example, "--dry-run", "--commit", dir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "transport:") {
		t.Errorf("stderr = %q, want transport error", stderr.String())
	}
}

func TestRunApplyHelpMentionsTransport(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help = %d", code)
	}
	out := stdout.String()
	for _, needle := range []string{"--pr", "--commit", "actionpins/apply-", "never force-push"} {
		if !strings.Contains(out, needle) {
			t.Errorf("help missing %q", needle)
		}
	}
}

func TestRunApplyMissingCatalog(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--catalog", filepath.Join(t.TempDir(), "nope.yaml"), t.TempDir()}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "error:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// writeFleetCatalog builds a catalog with two managed repos that use different
// action subsets (discovery-based inventory; never force unused actions).
func writeFleetCatalog(t *testing.T) (catalogPath, repoA, repoB string) {
	t.Helper()
	root := t.TempDir()
	repoA = filepath.Join(root, "repo-a")
	repoB = filepath.Join(root, "repo-b")
	for _, dir := range []string{repoA, repoB} {
		if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// repo-a uses checkout only (floating tag).
	aWF := `
jobs:
  j:
    steps:
      - uses: actions/checkout@v4
`
	if err := os.WriteFile(filepath.Join(repoA, ".github", "workflows", "ci.yml"), []byte(aWF), 0o600); err != nil {
		t.Fatal(err)
	}
	// repo-b uses setup-go only — checkout must not be injected on apply.
	bWF := `
jobs:
  j:
    steps:
      - uses: actions/setup-go@v5
`
	if err := os.WriteFile(filepath.Join(repoB, ".github", "workflows", "ci.yml"), []byte(bWF), 0o600); err != nil {
		t.Fatal(err)
	}

	catalogPath = filepath.Join(root, "catalog.yaml")
	// Use forward-slash-safe YAML quoting for paths (Windows may use \).
	content := fmt.Sprintf(`
actions:
  actions/checkout:
    version: v7.0.0
    sha: 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
  actions/setup-go:
    version: v6.5.0
    sha: 924ae3a1cded613372ab5595356fb5720e22ba16
policy:
  require_comment: true
repos:
  - name: fleet/repo-a
    path: %q
  - name: fleet/repo-b
    path: %q
`, repoA, repoB)
	if err := os.WriteFile(catalogPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return catalogPath, repoA, repoB
}

func TestRunScanAll(t *testing.T) {
	t.Parallel()
	catalogPath, _, _ := writeFleetCatalog(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"scan", "--all", "--catalog", catalogPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("scan --all = %d, stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "=== fleet/repo-a") || !strings.Contains(out, "=== fleet/repo-b") {
		t.Errorf("missing repo headers: %q", out)
	}
	if !strings.Contains(out, "actions/checkout") || !strings.Contains(out, "actions/setup-go") {
		t.Errorf("missing findings: %q", out)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"scan", "--all", "--format", "json", "--catalog", catalogPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("scan --all json = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name": "fleet/repo-a"`) {
		t.Errorf("json stdout = %q", stdout.String())
	}
}

func TestRunDiffAll(t *testing.T) {
	t.Parallel()
	catalogPath, _, _ := writeFleetCatalog(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"diff", "--all", "--catalog", catalogPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("diff --all with drift = %d, want 1; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "unpinned") {
		t.Errorf("want unpinned drift: %q", out)
	}
	if !strings.Contains(out, "fleet/repo-a") || !strings.Contains(out, "fleet/repo-b") {
		t.Errorf("missing repo headers: %q", out)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"diff", "--all", "--format", "json", "--catalog", catalogPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("diff --all json = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), `"drift": true`) {
		t.Errorf("json want drift true: %q", stdout.String())
	}
}

func TestRunApplyAllDiscoveryBased(t *testing.T) {
	t.Parallel()
	catalogPath, repoA, repoB := writeFleetCatalog(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--all", "--catalog", catalogPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply --all = %d, stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "fleet/repo-a") || !strings.Contains(out, "fleet/repo-b") {
		t.Errorf("missing repo headers: %q", out)
	}

	// repo-a should pin checkout only; never gain setup-go.
	aPath := filepath.Join(repoA, ".github", "workflows", "ci.yml")
	assertFileContains(t, aPath, []string{
		"actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0",
	})
	aData, err := os.ReadFile(aPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(aData), "setup-go") {
		t.Errorf("repo-a must not inject unused setup-go:\n%s", aData)
	}

	// repo-b should pin setup-go only; never gain checkout.
	bPath := filepath.Join(repoB, ".github", "workflows", "ci.yml")
	assertFileContains(t, bPath, []string{
		"actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0",
	})
	bData, err := os.ReadFile(bPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bData), "checkout") {
		t.Errorf("repo-b must not inject unused checkout:\n%s", bData)
	}
}

func TestRunApplyAllDryRun(t *testing.T) {
	t.Parallel()
	catalogPath, repoA, _ := writeFleetCatalog(t)
	aPath := filepath.Join(repoA, ".github", "workflows", "ci.yml")
	before, err := os.ReadFile(aPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--all", "--dry-run", "--format", "json", "--catalog", catalogPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply --all dry-run = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"dryRun": true`) {
		t.Errorf("json = %q", stdout.String())
	}
	assertFileEquals(t, aPath, string(before))
}

func TestRunAllRejectsPathArg(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"scan", "diff", "apply"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{cmd, "--all", "/some/path"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s --all with path = %d, want 2; stderr=%q", cmd, code, stderr.String())
		}
	}
}

func TestRunAllEmptyRepos(t *testing.T) {
	t.Parallel()
	// Example catalog has no repos: list (commented out).
	example := filepath.Join("..", "..", "examples", "catalog.yaml")
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("example catalog missing: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"scan", "--all", "--catalog", example}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("scan --all empty repos = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no managed repositories") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunCatalogValidateWithRepos(t *testing.T) {
	t.Parallel()
	catalogPath, _, _ := writeFleetCatalog(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"catalog", "validate", "--catalog", catalogPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("validate = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2 repos") {
		t.Errorf("stdout = %q, want 2 repos", stdout.String())
	}
}

func TestRunCheckUpdatesUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check-updates", "extra"}, &stdout, &stderr); code != 2 {
		t.Errorf("code = %d, want 2; stderr=%q", code, stderr.String())
	}
}

func TestRunProposeBumpUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"propose-bump"}, &stdout, &stderr); code != 2 {
		t.Errorf("code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunProposeBumpMissingCatalog(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	// Flags before the positional action (standard flag package).
	code := run([]string{"propose-bump", "--catalog", filepath.Join(t.TempDir(), "nope.yaml"), "actions/checkout"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "error:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunHelpListsUpdateCommands(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d", code)
	}
	out := stdout.String()
	for _, needle := range []string{"check-updates", "propose-bump", "approve-bump", "min_age"} {
		if !strings.Contains(out, needle) {
			t.Errorf("help missing %q", needle)
		}
	}
}

func TestRunApproveBumpExplicit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	content := `actions:
  actions/checkout:
    version: v4.0.0
    sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    approved_at: 2026-01-01
policy:
  min_age: 7d
  prefer: same-major
  require_comment: true
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Major jump without --allow-major must refuse when prefer=same-major.
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"approve-bump",
		"--catalog", path,
		"--version", "v5.0.0",
		"--sha", "dddddddddddddddddddddddddddddddddddddddd",
		"actions/checkout",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("major without allow = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "major jump") {
		t.Errorf("stderr = %q, want major jump", stderr.String())
	}

	// With --allow-major, catalog is updated.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"approve-bump",
		"--catalog", path,
		"--version", "v5.0.0",
		"--sha", "dddddddddddddddddddddddddddddddddddddddd",
		"--allow-major",
		"--format", "json",
		"checkout",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("approve-bump = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"toVersion": "v5.0.0"`) {
		t.Errorf("stdout = %q", stdout.String())
	}

	// Same-major bump without flag succeeds.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"approve-bump",
		"--catalog", path,
		"--version", "v5.1.0",
		"--sha", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"actions/checkout",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("same-major approve = %d, stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "approved bump") {
		t.Errorf("stdout = %q", stdout.String())
	}

	// Usage error: missing action.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"approve-bump", "--catalog", path}, &stdout, &stderr); code != 2 {
		t.Errorf("missing action = %d, want 2", code)
	}
}
