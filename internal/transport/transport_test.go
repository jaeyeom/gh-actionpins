package transport

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaeyeom/gh-actionpins/internal/apply"
	"github.com/jaeyeom/gh-actionpins/internal/diff"
)

// scriptedRunner returns canned outputs (or errors) keyed by "name arg0 arg1...".
// Partial keys can match prefixes via longest-match.
type scriptedRunner struct {
	t        *testing.T
	handlers map[string]func(dir string, args []string) ([]byte, error)
	calls    []string
}

func newScripted(t *testing.T) *scriptedRunner {
	t.Helper()
	return &scriptedRunner{
		t:        t,
		handlers: map[string]func(dir string, args []string) ([]byte, error){},
	}
}

func (s *scriptedRunner) on(name string, args []string, out string, err error) {
	key := cmdKey(name, args)
	s.handlers[key] = func(string, []string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		return []byte(out), nil
	}
}

func cmdKey(name string, args []string) string {
	return name + " " + strings.Join(args, " ")
}

func (s *scriptedRunner) Run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	key := cmdKey(name, args)
	s.calls = append(s.calls, key)
	// Exact match first, then longest prefix (for git add/commit with file lists).
	if fn, ok := s.handlers[key]; ok {
		return fn(dir, args)
	}
	var best string
	for k := range s.handlers {
		if strings.HasPrefix(key, k) && len(k) > len(best) {
			best = k
		}
	}
	if best != "" {
		return s.handlers[best](dir, args)
	}
	return nil, fmt.Errorf("unexpected command: %s (dir=%s)", key, dir)
}

func sampleChanges() []apply.Change {
	return []apply.Change{
		{
			File:           ".github/workflows/ci.yml",
			Line:           10,
			Action:         "actions/checkout",
			OldUses:        "actions/checkout@v4",
			NewUses:        "actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa # v7.0.0",
			OldStatus:      diff.StatusUnpinned,
			CatalogVersion: "v7.0.0",
			CatalogSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			File:           ".github/workflows/ci.yml",
			Line:           14,
			Action:         "actions/setup-go",
			OldUses:        "actions/setup-go@v5",
			NewUses:        "actions/setup-go@bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb # v6.5.0",
			OldStatus:      diff.StatusUnpinned,
			CatalogVersion: "v6.5.0",
			CatalogSHA:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	}
}

func TestDeliverCommitSuccess(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "true\n", nil)
	r.on("git", []string{"add", "--"}, "", nil) // prefix match
	r.on("git", []string{"commit", "-m"}, "", nil)
	r.on("git", []string{"rev-parse", "HEAD"}, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n", nil)

	res, err := Deliver(context.Background(), Options{
		Mode:        ModeCommit,
		Root:        t.TempDir(),
		Changes:     sampleChanges(),
		CatalogPath: "examples/catalog.yaml",
		Runner:      r,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.Skipped || res.CommitSHA != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("result = %+v", res)
	}
	if res.Mode != ModeCommit || res.PRURL != "" {
		t.Errorf("unexpected mode/url: %+v", res)
	}
	// Ensure no force-push or pr create in commit mode.
	for _, c := range r.calls {
		if strings.HasPrefix(c, "git push") || strings.HasPrefix(c, "gh pr create") {
			t.Errorf("commit mode must not push/pr: %s", c)
		}
		if strings.Contains(c, " --force") || strings.HasSuffix(c, "--force") {
			t.Errorf("must never force: %s", c)
		}
	}
}

func TestDeliverPRSuccess(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "true\n", nil)
	r.on("gh", []string{"auth", "status"}, "ok\n", nil)
	r.on("gh", []string{"repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"}, "owner/repo\n", nil)
	r.on("gh", []string{"repo", "view", "--json", "defaultBranchRef", "-q", ".defaultBranchRef.name"}, "main\n", nil)
	r.on("git", []string{"switch", "-c"}, "", nil)
	r.on("git", []string{"add", "--"}, "", nil)
	r.on("git", []string{"commit", "-m"}, "", nil)
	r.on("git", []string{"rev-parse", "HEAD"}, "cafebabecafebabecafebabecafebabecafebabe\n", nil)
	r.on("git", []string{"push", "-u", "origin", "HEAD"}, "ok\n", nil)
	r.on("gh", []string{"pr", "create"}, "https://github.com/owner/repo/pull/42\n", nil)

	fixed := time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)
	res, err := Deliver(context.Background(), Options{
		Mode:        ModePR,
		Root:        t.TempDir(),
		Changes:     sampleChanges(),
		CatalogPath: "/tmp/catalog.yaml",
		Runner:      r,
		Now:         func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	wantBranch := "actionpins/apply-20260715-123000"
	if res.Branch != wantBranch || res.Base != "main" {
		t.Errorf("branch/base = %q / %q", res.Branch, res.Base)
	}
	if res.PRURL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("prUrl = %q", res.PRURL)
	}
	if res.CommitSHA != "cafebabecafebabecafebabecafebabecafebabe" {
		t.Errorf("sha = %q", res.CommitSHA)
	}
	assertPRCallSequence(t, r.calls, wantBranch)
}

func assertPRCallSequence(t *testing.T, calls []string, wantBranch string) {
	t.Helper()
	var sawSwitch, sawPush, sawPR bool
	for _, c := range calls {
		if strings.HasPrefix(c, "git push") && (strings.Contains(c, " --force") || strings.Contains(c, " -f")) {
			t.Errorf("must never force-push: %s", c)
		}
		if strings.HasPrefix(c, "git switch -c ") {
			sawSwitch = true
			if !strings.Contains(c, wantBranch) {
				t.Errorf("switch branch: %s", c)
			}
		}
		if c == "git push -u origin HEAD" {
			sawPush = true
		}
		if strings.HasPrefix(c, "gh pr create") {
			sawPR = true
			if !strings.Contains(c, "--base") || !strings.Contains(c, "main") {
				t.Errorf("pr create missing base main: %s", c)
			}
		}
	}
	if !sawSwitch || !sawPush || !sawPR {
		t.Errorf("missing steps switch=%v push=%v pr=%v calls=%v", sawSwitch, sawPush, sawPR, calls)
	}
}

func TestDeliverPRDryRun(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "true\n", nil)
	r.on("gh", []string{"auth", "status"}, "ok\n", nil)
	r.on("gh", []string{"repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"}, "o/r\n", nil)
	r.on("gh", []string{"repo", "view", "--json", "defaultBranchRef", "-q", ".defaultBranchRef.name"}, "master\n", nil)

	res, err := Deliver(context.Background(), Options{
		Mode:    ModePR,
		Root:    t.TempDir(),
		Changes: sampleChanges(),
		DryRun:  true,
		Runner:  r,
		Now:     func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.DryRun || res.PRURL != "" || res.CommitSHA != "" {
		t.Fatalf("dry-run should not commit/pr: %+v", res)
	}
	if res.Branch != "actionpins/apply-20260102-030405" || res.Base != "master" {
		t.Errorf("branch/base = %q / %q", res.Branch, res.Base)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "switch") || strings.Contains(c, "commit") || strings.Contains(c, "push") || strings.Contains(c, "pr create") {
			t.Errorf("dry-run must not mutate: %s", c)
		}
	}
}

func TestDeliverSkippedNoChanges(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	res, err := Deliver(context.Background(), Options{
		Mode:    ModeCommit,
		Root:    t.TempDir(),
		Changes: nil,
		Runner:  r,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("want skipped, got %+v", res)
	}
	if len(r.calls) != 0 {
		t.Errorf("no commands expected when skipped: %v", r.calls)
	}
}

func TestDeliverNotGitRepo(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "", fmt.Errorf("fatal: not a git repository"))

	_, err := Deliver(context.Background(), Options{
		Mode:    ModeCommit,
		Root:    t.TempDir(),
		Changes: sampleChanges(),
		Runner:  r,
	})
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("err = %v, want git repo error", err)
	}
}

func TestDeliverPRMissingAuth(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "true\n", nil)
	r.on("gh", []string{"auth", "status"}, "", fmt.Errorf("not logged in"))

	_, err := Deliver(context.Background(), Options{
		Mode:    ModePR,
		Root:    t.TempDir(),
		Changes: sampleChanges(),
		Runner:  r,
	})
	if err == nil || !strings.Contains(err.Error(), "gh auth") {
		t.Fatalf("err = %v, want gh auth error", err)
	}
}

func TestDeliverPRMissingRepoContext(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "true\n", nil)
	r.on("gh", []string{"auth", "status"}, "ok\n", nil)
	r.on("gh", []string{"repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"}, "", fmt.Errorf("no git remotes found"))

	_, err := Deliver(context.Background(), Options{
		Mode:    ModePR,
		Root:    t.TempDir(),
		Changes: sampleChanges(),
		Runner:  r,
	})
	if err == nil || !strings.Contains(err.Error(), "gh repo context") {
		t.Fatalf("err = %v, want repo context error", err)
	}
}

func TestDeliverInvalidMode(t *testing.T) {
	t.Parallel()
	_, err := Deliver(context.Background(), Options{
		Mode:    ModeNone,
		Root:    t.TempDir(),
		Changes: sampleChanges(),
	})
	if err == nil {
		t.Fatal("want error for empty mode")
	}
}

func TestDeliverCommitDryRun(t *testing.T) {
	t.Parallel()
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "true\n", nil)

	res, err := Deliver(context.Background(), Options{
		Mode:    ModeCommit,
		Root:    t.TempDir(),
		Changes: sampleChanges(),
		DryRun:  true,
		Runner:  r,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.DryRun || res.CommitSHA != "" {
		t.Fatalf("got %+v", res)
	}
}

func TestCommitBodyListsPins(t *testing.T) {
	t.Parallel()
	body := commitBody("cat.yaml", sampleChanges())
	for _, want := range []string{
		"cat.yaml",
		"actions/checkout@",
		"actions/setup-go@",
		"never force-pushed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestWriteTableAndJSON(t *testing.T) {
	t.Parallel()
	res := &Result{
		Mode:      ModePR,
		Branch:    "actionpins/apply-1",
		Base:      "main",
		CommitSHA: "abcdef0123456789",
		PRURL:     "https://example.com/pr/1",
	}
	var buf bytes.Buffer
	if err := Write(&buf, res, FormatTable); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "transport: pr") {
		t.Errorf("table = %q", buf.String())
	}
	buf.Reset()
	if err := Write(&buf, res, FormatJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"prUrl"`) {
		t.Errorf("json = %q", buf.String())
	}
}

func TestUniqueFilesDedupAndSort(t *testing.T) {
	t.Parallel()
	files := uniqueFiles([]apply.Change{
		{File: "b.yml"},
		{File: "a.yml"},
		{File: "b.yml"},
		{File: ""},
	})
	if len(files) != 2 || files[0] != "a.yml" || files[1] != "b.yml" {
		t.Fatalf("files = %v", files)
	}
}

func TestFirstURL(t *testing.T) {
	t.Parallel()
	in := "Creating pull request\nhttps://github.com/o/r/pull/9\n"
	if got := firstURL(in); got != "https://github.com/o/r/pull/9" {
		t.Errorf("got %q", got)
	}
	if firstURL("no url here") != "" {
		t.Error("want empty")
	}
}

func TestDeliverUsesAbsRoot(t *testing.T) {
	t.Parallel()
	// Empty root is rejected; non-empty roots are Abs()'d before git runs.
	r := newScripted(t)
	r.on("git", []string{"rev-parse", "--is-inside-work-tree"}, "true\n", nil)
	r.on("git", []string{"add", "--"}, "", nil)
	r.on("git", []string{"commit", "-m"}, "", nil)
	r.on("git", []string{"rev-parse", "HEAD"}, "1111111111111111111111111111111111111111\n", nil)

	dir := t.TempDir()
	// Pass a path that Abs will normalize (trailing slash / .).
	root := filepath.Join(dir, ".")
	res, err := Deliver(context.Background(), Options{
		Mode:    ModeCommit,
		Root:    root,
		Changes: sampleChanges(),
		Runner:  r,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.CommitSHA == "" {
		t.Fatalf("want commit sha, got %+v", res)
	}

	_, err = Deliver(context.Background(), Options{
		Mode:    ModeCommit,
		Root:    "",
		Changes: sampleChanges(),
		Runner:  r,
	})
	if err == nil || !strings.Contains(err.Error(), "root is empty") {
		t.Fatalf("empty root err = %v", err)
	}
}
