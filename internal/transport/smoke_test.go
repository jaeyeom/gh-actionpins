package transport

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaeyeom/gh-actionpins/internal/apply"
	"github.com/jaeyeom/gh-actionpins/internal/diff"
)

// TestSmokeLocalCommit uses a real git repository to verify --commit transport
// (fixture remote flow without network): init repo, rewrite-like file change,
// Deliver ModeCommit, assert commit exists and only the pin file is in it.
func TestSmokeLocalCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := initSmokeRepo(t)
	wf := filepath.Join(dir, ".github", "workflows", "ci.yml")
	pinned := "jobs:\n  j:\n    steps:\n      - uses: actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa # v7.0.0\n"
	if err := os.WriteFile(wf, []byte(pinned), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Deliver(context.Background(), Options{
		Mode: ModeCommit,
		Root: dir,
		Changes: []apply.Change{{
			File:      ".github/workflows/ci.yml",
			Line:      4,
			Action:    "actions/checkout",
			OldUses:   "actions/checkout@v4",
			NewUses:   "actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa # v7.0.0",
			OldStatus: diff.StatusUnpinned,
		}},
		CatalogPath: "examples/catalog.yaml",
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.CommitSHA == "" || res.Skipped {
		t.Fatalf("result = %+v", res)
	}
	assertSmokeCommit(t, dir)
}

func initSmokeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitDir(t, dir, "init")
	runGitDir(t, dir, "checkout", "-b", "main")

	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "jobs:\n  j:\n    steps:\n      - uses: actions/checkout@v4\n"
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unrelated dirty file that must NOT be committed.
	if err := os.WriteFile(filepath.Join(dir, "noise.txt"), []byte("leave me out\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitDir(t, dir, "add", ".github/workflows/ci.yml")
	runGitDir(t, dir, "commit", "-m", "initial")
	return dir
}

func runGitDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=actionpins-test",
		"GIT_AUTHOR_EMAIL=actionpins@example.com",
		"GIT_COMMITTER_NAME=actionpins-test",
		"GIT_COMMITTER_EMAIL=actionpins@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func assertSmokeCommit(t *testing.T, dir string) {
	t.Helper()
	subject := gitOut(t, dir, "log", "-1", "--pretty=%s")
	if subject != DefaultCommitSubject {
		t.Errorf("subject = %q, want %q", subject, DefaultCommitSubject)
	}
	files := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	if files != ".github/workflows/ci.yml" {
		t.Errorf("committed files = %q, want only workflow", files)
	}
	status := gitOut(t, dir, "status", "--porcelain")
	if !strings.Contains(status, "noise.txt") {
		t.Errorf("status should still list noise.txt: %q", status)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
