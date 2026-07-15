// Package transport delivers apply rewrites as a local commit or a reviewable
// PR via the GitHub CLI. It never force-pushes and never updates the default
// branch directly.
package transport

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jaeyeom/gh-actionpins/internal/apply"
)

// Mode selects how pin rewrites are published after local file updates.
type Mode string

const (
	// ModeNone leaves files on disk only (apply default).
	ModeNone Mode = ""
	// ModeCommit creates a local git commit of rewritten files only.
	ModeCommit Mode = "commit"
	// ModePR creates a branch, commits, pushes (no force), and opens a PR via gh.
	ModePR Mode = "pr"
)

// BranchPrefix is the fixed prefix for PR branches.
// Full name: actionpins/apply-YYYYMMDD-HHMMSS (UTC).
const BranchPrefix = "actionpins/apply-"

// DefaultCommitSubject is the conventional commit subject for pin applies.
const DefaultCommitSubject = "chore: pin GitHub Actions to trusted catalog SHAs"

// Result is the outcome of a transport delivery.
type Result struct {
	// Mode is commit or pr.
	Mode Mode `json:"mode"`
	// DryRun is true when no git/gh side effects were performed.
	DryRun bool `json:"dryRun"`
	// Skipped is true when there was nothing to deliver (no file changes).
	Skipped bool `json:"skipped,omitempty"`
	// Branch is the branch name used for ModePR (empty for commit mode).
	Branch string `json:"branch,omitempty"`
	// Base is the PR base branch (default branch) for ModePR.
	Base string `json:"base,omitempty"`
	// CommitSHA is the created commit when not dry-run and not skipped.
	CommitSHA string `json:"commitSha,omitempty"`
	// PRURL is the opened pull request URL for ModePR.
	PRURL string `json:"prUrl,omitempty"`
	// Subject is the commit/PR title used.
	Subject string `json:"subject,omitempty"`
	// Message is a short human-readable status.
	Message string `json:"message,omitempty"`
}

// Options controls transport delivery.
type Options struct {
	// Mode is commit or pr (required).
	Mode Mode
	// Root is the repository root (absolute path preferred).
	Root string
	// Changes are the apply rewrites; only their files are staged/committed.
	Changes []apply.Change
	// CatalogPath is included in the commit/PR body when set.
	CatalogPath string
	// DryRun plans transport without running git/gh write operations.
	DryRun bool
	// Runner executes commands; defaults to a real exec runner when nil.
	Runner Runner
	// Now overrides the clock for branch timestamps (tests).
	Now func() time.Time
	// Branch, when set, overrides the generated PR branch name.
	Branch string
	// Subject overrides the commit/PR title.
	Subject string
}

// Deliver publishes apply changes according to opts.Mode.
//
// Conventions:
//   - Branch (PR mode): actionpins/apply-YYYYMMDD-HHMMSS (UTC)
//   - Commit subject: chore: pin GitHub Actions to trusted catalog SHAs
//   - Only files listed in Changes are staged/committed
//   - Push uses git push -u origin HEAD (never --force)
//   - PR is opened with gh pr create against the repo default branch
//
// Failures are safe: missing git repo, missing gh auth/repo context, or a
// dirty index that cannot be committed surfaces a clear error without
// force-pushing the default branch.
func Deliver(ctx context.Context, opts Options) (*Result, error) {
	if opts.Mode != ModeCommit && opts.Mode != ModePR {
		return nil, fmt.Errorf("transport mode %q: want %q or %q", opts.Mode, ModeCommit, ModePR)
	}
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	runner := opts.Runner
	if runner == nil {
		runner = DefaultRunner
	}
	subject := strings.TrimSpace(opts.Subject)
	if subject == "" {
		subject = DefaultCommitSubject
	}

	files := uniqueFiles(opts.Changes)
	res := &Result{Mode: opts.Mode, DryRun: opts.DryRun, Subject: subject}
	if len(files) == 0 {
		res.Skipped = true
		res.Message = "no pin changes to deliver"
		return res, nil
	}
	if err := requireGitRepo(ctx, runner, root); err != nil {
		return nil, err
	}

	body := commitBody(opts.CatalogPath, opts.Changes)
	switch opts.Mode {
	case ModePR:
		return deliverPR(ctx, runner, root, files, subject, body, opts, res)
	default:
		return deliverCommit(ctx, runner, root, files, subject, body, opts.DryRun, res)
	}
}

func resolveRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("transport root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	return abs, nil
}

func deliverCommit(ctx context.Context, runner Runner, root string, files []string, subject, body string, dryRun bool, res *Result) (*Result, error) {
	if dryRun {
		res.Message = "would create local commit of pin rewrites"
		return res, nil
	}
	sha, err := commitFiles(ctx, runner, root, files, subject, body)
	if err != nil {
		return nil, err
	}
	res.CommitSHA = sha
	res.Message = fmt.Sprintf("created local commit %s", shortSHA(sha))
	return res, nil
}

func deliverPR(ctx context.Context, runner Runner, root string, files []string, subject, body string, opts Options, res *Result) (*Result, error) {
	if err := requireGHRepo(ctx, runner, root); err != nil {
		return nil, err
	}
	base, err := defaultBranch(ctx, runner, root)
	if err != nil {
		return nil, err
	}
	res.Base = base

	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		now := opts.Now
		if now == nil {
			now = time.Now
		}
		branch = BranchPrefix + now().UTC().Format("20060102-150405")
	}
	res.Branch = branch

	if opts.DryRun {
		res.Message = fmt.Sprintf("would create branch %s, commit, push, and open PR against %s", branch, base)
		return res, nil
	}

	// Side branch only — never push commits onto the default branch via this path.
	if err := runGit(ctx, runner, root, "switch", "-c", branch); err != nil {
		return nil, fmt.Errorf("create branch %s: %w", branch, err)
	}
	sha, err := commitFiles(ctx, runner, root, files, subject, body)
	if err != nil {
		return nil, err
	}
	res.CommitSHA = sha

	if err := runGit(ctx, runner, root, "push", "-u", "origin", "HEAD"); err != nil {
		return nil, fmt.Errorf("push branch %s (never force-pushes default branch): %w", branch, err)
	}
	prURL, err := createPR(ctx, runner, root, subject, body, base)
	if err != nil {
		return nil, err
	}
	res.PRURL = prURL
	res.Message = fmt.Sprintf("opened PR %s", prURL)
	return res, nil
}

func uniqueFiles(changes []apply.Change) []string {
	seen := map[string]struct{}{}
	var files []string
	for _, c := range changes {
		f := filepath.ToSlash(strings.TrimSpace(c.File))
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

func commitBody(catalogPath string, changes []apply.Change) string {
	var b strings.Builder
	b.WriteString("Apply trusted GitHub Actions pins from the actionpins catalog.\n")
	b.WriteString("\n")
	b.WriteString("This change rewrites workflow uses: lines to catalog SHAs only;\n")
	b.WriteString("unknown, local, and Docker actions are left unchanged.\n")
	if catalogPath != "" {
		b.WriteString("\n")
		b.WriteString("Catalog: ")
		b.WriteString(catalogPath)
		b.WriteString("\n")
	}
	if len(changes) > 0 {
		b.WriteString("\n")
		b.WriteString("Pins:\n")
		type pin struct {
			action, newUses string
		}
		seen := map[string]struct{}{}
		var pins []pin
		for _, c := range changes {
			key := c.Action + "\x00" + c.NewUses
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			pins = append(pins, pin{action: c.Action, newUses: c.NewUses})
		}
		sort.Slice(pins, func(i, j int) bool {
			if pins[i].action != pins[j].action {
				return pins[i].action < pins[j].action
			}
			return pins[i].newUses < pins[j].newUses
		})
		for _, p := range pins {
			b.WriteString("- ")
			b.WriteString(p.newUses)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString("Generated by gh-actionpins. Review before merge; default branch is never force-pushed.\n")
	return b.String()
}

func shortSHA(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

func requireGitRepo(ctx context.Context, r Runner, root string) error {
	out, err := r.Run(ctx, root, "git", "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("not a git repository (or git unavailable) at %s: %w", root, err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("not a git repository at %s", root)
	}
	return nil
}

// requireGHRepo checks gh authentication and that the cwd maps to a GitHub repo.
func requireGHRepo(ctx context.Context, r Runner, root string) error {
	if _, err := r.Run(ctx, root, "gh", "auth", "status"); err != nil {
		return fmt.Errorf("gh auth missing or invalid (run: gh auth login): %w", err)
	}
	// Confirms gh can resolve the git remote to a GitHub repository.
	if _, err := r.Run(ctx, root, "gh", "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"); err != nil {
		return fmt.Errorf("gh repo context missing (is origin a GitHub remote?): %w", err)
	}
	return nil
}

func defaultBranch(ctx context.Context, r Runner, root string) (string, error) {
	out, err := r.Run(ctx, root, "gh", "repo", "view", "--json", "defaultBranchRef", "-q", ".defaultBranchRef.name")
	if err != nil {
		return "", fmt.Errorf("resolve default branch via gh: %w", err)
	}
	base := strings.TrimSpace(string(out))
	if base == "" {
		return "", fmt.Errorf("resolve default branch: empty name from gh")
	}
	return base, nil
}

func commitFiles(ctx context.Context, r Runner, root string, files []string, subject, body string) (string, error) {
	// Stage only the rewritten workflow files.
	args := append([]string{"add", "--"}, files...)
	if err := runGit(ctx, r, root, args...); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	// Commit only those paths so unrelated staged files are not included.
	msg := subject
	if strings.TrimSpace(body) != "" {
		msg = subject + "\n\n" + body
	}
	commitArgs := append([]string{"commit", "-m", msg, "--"}, files...)
	if err := runGit(ctx, r, root, commitArgs...); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	out, err := r.Run(ctx, root, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("git rev-parse HEAD: empty sha")
	}
	return sha, nil
}

func createPR(ctx context.Context, r Runner, root, title, body, base string) (string, error) {
	out, err := r.Run(ctx, root, "gh", "pr", "create",
		"--title", title,
		"--body", body,
		"--base", base,
	)
	if err != nil {
		return "", fmt.Errorf("gh pr create: %w", err)
	}
	// gh pr create prints the PR URL on stdout.
	url := firstURL(string(out))
	if url == "" {
		url = strings.TrimSpace(string(out))
	}
	if url == "" {
		return "", fmt.Errorf("gh pr create: empty output (no PR URL)")
	}
	return url, nil
}

func firstURL(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") || strings.HasPrefix(line, "http://") {
			return line
		}
	}
	return ""
}

func runGit(ctx context.Context, r Runner, root string, args ...string) error {
	if _, err := r.Run(ctx, root, "git", args...); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
