package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RemoteRelease is one upstream release or tag with an optional publish time.
type RemoteRelease struct {
	// Tag is the git tag name (e.g. "v4.1.2").
	Tag string
	// PublishedAt is when the release/tag became available (zero if unknown).
	PublishedAt time.Time
	// Prerelease is true when GitHub marks the release as pre-release.
	Prerelease bool
	// Draft is true for unpublished drafts (normally filtered out by the client).
	Draft bool
	// SHA is the resolved commit when already known (tags API); may be empty.
	SHA string
}

// Lookup fetches upstream tags/releases and resolves refs to commit SHAs.
// Implementations may call the GitHub API (via gh) or a test double.
type Lookup interface {
	// ListReleases returns non-draft releases for owner/repo (newest first preferred).
	// When the repo has no GitHub Releases, it may fall back to tags.
	ListReleases(ctx context.Context, owner, repo string) ([]RemoteRelease, error)
	// ResolveSHA returns the full commit SHA for ref (tag, branch, or SHA).
	ResolveSHA(ctx context.Context, owner, repo, ref string) (string, error)
}

// GHLookup uses the GitHub CLI (`gh api`) so a gh extension inherits auth.
type GHLookup struct {
	// Runner executes a command; defaults to exec.CommandContext when nil.
	// Signature matches (*exec.Cmd).CombinedOutput for easy testing.
	Runner func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (g GHLookup) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if g.Runner != nil {
		return g.Runner(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out, nil
}

// ListReleases implements Lookup using GitHub Releases, falling back to tags.
func (g GHLookup) ListReleases(ctx context.Context, owner, repo string) ([]RemoteRelease, error) {
	path := fmt.Sprintf("repos/%s/%s/releases?per_page=100", owner, repo)
	out, err := g.run(ctx, "gh", "api", "--paginate", path)
	if err != nil {
		// Fall back to tags when releases endpoint fails or is empty later.
		return g.listTags(ctx, owner, repo)
	}
	// GitHub REST API uses snake_case field names.
	var raw []struct {
		TagName     string `json:"tag_name"`     //nolint:tagliatelle // GitHub API
		PublishedAt string `json:"published_at"` //nolint:tagliatelle // GitHub API
		CreatedAt   string `json:"created_at"`   //nolint:tagliatelle // GitHub API
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("decode releases for %s/%s: %w", owner, repo, err)
	}
	if len(raw) == 0 {
		return g.listTags(ctx, owner, repo)
	}
	outRel := make([]RemoteRelease, 0, len(raw))
	for _, r := range raw {
		if r.Draft || strings.TrimSpace(r.TagName) == "" {
			continue
		}
		published := parseGitHubTime(r.PublishedAt)
		if published.IsZero() {
			published = parseGitHubTime(r.CreatedAt)
		}
		outRel = append(outRel, RemoteRelease{
			Tag:         r.TagName,
			PublishedAt: published,
			Prerelease:  r.Prerelease,
		})
	}
	if len(outRel) == 0 {
		return g.listTags(ctx, owner, repo)
	}
	return outRel, nil
}

func (g GHLookup) listTags(ctx context.Context, owner, repo string) ([]RemoteRelease, error) {
	path := fmt.Sprintf("repos/%s/%s/tags?per_page=100", owner, repo)
	out, err := g.run(ctx, "gh", "api", "--paginate", path)
	if err != nil {
		return nil, fmt.Errorf("list tags for %s/%s: %w", owner, repo, err)
	}
	var raw []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("decode tags for %s/%s: %w", owner, repo, err)
	}
	rels := make([]RemoteRelease, 0, len(raw))
	for _, t := range raw {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		// Tag list has no publish date; resolve commit date for age checks.
		published, _ := g.commitDate(ctx, owner, repo, t.Commit.SHA)
		rels = append(rels, RemoteRelease{
			Tag:         t.Name,
			PublishedAt: published,
			SHA:         t.Commit.SHA,
		})
	}
	return rels, nil
}

func (g GHLookup) commitDate(ctx context.Context, owner, repo, sha string) (time.Time, error) {
	if sha == "" {
		return time.Time{}, fmt.Errorf("empty sha")
	}
	path := fmt.Sprintf("repos/%s/%s/commits/%s", owner, repo, sha)
	out, err := g.run(ctx, "gh", "api", path, "--jq", ".commit.committer.date")
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(strings.Trim(string(out), "\"\n"))
	return parseGitHubTime(s), nil
}

// ResolveSHA implements Lookup.
func (g GHLookup) ResolveSHA(ctx context.Context, owner, repo, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty ref")
	}
	// Commits endpoint accepts tags, branches, and SHAs.
	path := fmt.Sprintf("repos/%s/%s/commits/%s", owner, repo, ref)
	out, err := g.run(ctx, "gh", "api", path, "--jq", ".sha")
	if err != nil {
		return "", fmt.Errorf("resolve %s/%s@%s: %w", owner, repo, ref, err)
	}
	sha := strings.TrimSpace(strings.Trim(string(out), "\"\n"))
	if len(sha) != 40 {
		return "", fmt.Errorf("resolve %s/%s@%s: unexpected sha %q", owner, repo, ref, sha)
	}
	return strings.ToLower(sha), nil
}

func parseGitHubTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05Z", s); err == nil {
		return t
	}
	return time.Time{}
}

// ParseActionRepo extracts GitHub owner/repo from a catalog action key.
// Keys may be "owner/repo" or "owner/repo/path/to/action".
func ParseActionRepo(action string) (owner, repo string, err error) {
	action = strings.TrimSpace(action)
	parts := strings.Split(action, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("action %q: want owner/repo[/path]", action)
	}
	return parts[0], parts[1], nil
}
