// Package apply rewrites workflow uses: lines to catalog trusted pin form.
//
// Only catalogued actions with drift (mismatch or unpinned) are rewritten.
// Unknown, local, and Docker uses are left alone. Updates are local files only.
package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
	"github.com/jaeyeom/gh-actionpins/internal/diff"
	"github.com/jaeyeom/gh-actionpins/internal/scan"
)

// Change is one planned or applied uses: rewrite.
type Change struct {
	// File is the path relative to the scan root (slash-separated).
	File string `json:"file"`
	// Line is the 1-based line of the uses value.
	Line int `json:"line"`
	// Action is owner/name or owner/name/path.
	Action string `json:"action"`
	// OldUses is the uses value as written before apply (action@ref).
	OldUses string `json:"oldUses"`
	// NewUses is the trusted pin form (action@sha, optionally with # version).
	NewUses string `json:"newUses"`
	// OldStatus is the diff classification before apply.
	OldStatus diff.Status `json:"oldStatus"`
	// CatalogVersion is the trusted version tag.
	CatalogVersion string `json:"catalogVersion,omitempty"`
	// CatalogSHA is the trusted commit SHA.
	CatalogSHA string `json:"catalogSha,omitempty"`
}

// Skip is a finding left unchanged (unknown or already ok).
type Skip struct {
	File   string      `json:"file"`
	Line   int         `json:"line"`
	Action string      `json:"action"`
	Uses   string      `json:"uses"`
	Status diff.Status `json:"status"`
	Reason string      `json:"reason"`
}

// Result is the outcome of planning or applying pin rewrites.
type Result struct {
	// Root is the absolute path that was scanned.
	Root string `json:"root"`
	// CatalogPath is the catalog file path when known.
	CatalogPath string `json:"catalogPath,omitempty"`
	// DryRun is true when no files were written.
	DryRun bool `json:"dryRun"`
	// Changes lists rewrites (planned when DryRun, applied otherwise).
	Changes []Change `json:"changes"`
	// Skipped lists findings not rewritten.
	Skipped []Skip `json:"skipped"`
}

// Options controls apply behavior.
type Options struct {
	// CatalogPath is recorded on the result only.
	CatalogPath string
	// DryRun plans changes without writing files.
	DryRun bool
	// EnforceComment, when non-nil, overrides catalog policy.require_comment.
	EnforceComment *bool
}

// Run scans root, compares against cat, and rewrites drifted catalogued pins.
// Unknown actions are skipped (not corrupted). Local/Docker uses never appear
// in scan findings. When opts.DryRun is true, files are not modified.
func Run(cat *catalog.Catalog, root string, opts Options) (*Result, error) {
	if cat == nil {
		return nil, fmt.Errorf("catalog is nil")
	}

	scanResult, err := scan.Scan(root)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	diffRes, err := diff.Compare(cat, scanResult, diff.Options{
		CatalogPath:    opts.CatalogPath,
		EnforceComment: opts.EnforceComment,
	})
	if err != nil {
		return nil, fmt.Errorf("diff: %w", err)
	}

	requireComment := cat.Policy.RequireCommentEnabled()
	if opts.EnforceComment != nil {
		requireComment = *opts.EnforceComment
	}

	res := &Result{
		Root:        scanResult.Root,
		CatalogPath: opts.CatalogPath,
		DryRun:      opts.DryRun,
		Changes:     []Change{},
		Skipped:     []Skip{},
	}

	// Group planned line rewrites by file.
	byFile := map[string][]Change{}
	for _, e := range diffRes.Entries {
		switch e.Status {
		case diff.StatusOK:
			res.Skipped = append(res.Skipped, Skip{
				File: e.File, Line: e.Line, Action: e.Action, Uses: e.Uses,
				Status: e.Status, Reason: "already matches catalog",
			})
		case diff.StatusUnknown:
			res.Skipped = append(res.Skipped, Skip{
				File: e.File, Line: e.Line, Action: e.Action, Uses: e.Uses,
				Status: e.Status, Reason: "action not in catalog",
			})
		case diff.StatusMismatch, diff.StatusUnpinned:
			newUses := pinForm(e.Action, e.CatalogSHA, e.CatalogVersion, requireComment)
			ch := Change{
				File:           e.File,
				Line:           e.Line,
				Action:         e.Action,
				OldUses:        e.Uses,
				NewUses:        newUses,
				OldStatus:      e.Status,
				CatalogVersion: e.CatalogVersion,
				CatalogSHA:     e.CatalogSHA,
			}
			res.Changes = append(res.Changes, ch)
			byFile[e.File] = append(byFile[e.File], ch)
		default:
			res.Skipped = append(res.Skipped, Skip{
				File: e.File, Line: e.Line, Action: e.Action, Uses: e.Uses,
				Status: e.Status, Reason: "unhandled status",
			})
		}
	}

	sortChanges(res.Changes)
	sortSkips(res.Skipped)

	if opts.DryRun || len(byFile) == 0 {
		return res, nil
	}

	for file, changes := range byFile {
		if err := rewriteFile(scanResult.Root, file, changes); err != nil {
			return res, err
		}
	}
	return res, nil
}

// pinForm builds the trusted uses value: action@sha or action@sha # version.
func pinForm(action, sha, version string, requireComment bool) string {
	pin := action + "@" + sha
	if requireComment {
		return pin + " # " + strings.TrimSpace(version)
	}
	return pin
}

func rewriteFile(root, relFile string, changes []Change) error {
	path := filepath.Join(root, filepath.FromSlash(relFile))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", relFile, err)
	}

	// Preserve original line endings as much as practical.
	content := string(data)
	endsWithNL := strings.HasSuffix(content, "\n")
	lines := splitLines(content)

	// Apply by line number (1-based). Multiple changes on the same line are
	// applied left-to-right after sorting by line.
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Line < changes[j].Line
	})

	for _, ch := range changes {
		if ch.Line < 1 || ch.Line > len(lines) {
			return fmt.Errorf("%s: line %d out of range (file has %d lines)", relFile, ch.Line, len(lines))
		}
		idx := ch.Line - 1
		rewritten, err := rewriteUsesLine(lines[idx], ch.OldUses, ch.NewUses)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", relFile, ch.Line, err)
		}
		lines[idx] = rewritten
	}

	out := strings.Join(lines, "\n")
	if endsWithNL {
		out += "\n"
	}
	// Keep original file mode when possible.
	info, err := os.Stat(path)
	mode := os.FileMode(0o600)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(out), mode); err != nil {
		return fmt.Errorf("write %s: %w", relFile, err)
	}
	return nil
}

// splitLines splits on \n and strips a trailing empty segment only when the
// file ended with a newline (so Join can re-add it). Also strips \r for CRLF.
func splitLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return []string{}
	}
	parts := strings.Split(content, "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

// rewriteUsesLine replaces the uses: value on a single workflow line.
// Prefix (indent, list marker, "uses:") is preserved; the value and any
// trailing comment are replaced with newUses.
func rewriteUsesLine(line, oldUses, newUses string) (string, error) {
	const key = "uses:"
	idx := strings.Index(line, key)
	if idx < 0 {
		return "", fmt.Errorf("no %q key on line", key)
	}
	prefix := line[:idx+len(key)]
	rest := line[idx+len(key):]

	// Preserve spacing between uses: and the value.
	spaceEnd := 0
	for spaceEnd < len(rest) && (rest[spaceEnd] == ' ' || rest[spaceEnd] == '\t') {
		spaceEnd++
	}
	space := rest[:spaceEnd]
	valuePart := rest[spaceEnd:]

	// Verify the line still refers to the expected uses (action@ref), allowing
	// optional quotes and an optional trailing comment.
	if !valueMatchesOldUses(valuePart, oldUses) {
		return "", fmt.Errorf("line does not contain expected uses %q", oldUses)
	}

	return prefix + space + newUses, nil
}

// valueMatchesOldUses reports whether the uses value (with optional quotes
// and trailing comment) matches the scanned oldUses string.
func valueMatchesOldUses(valuePart, oldUses string) bool {
	v := strings.TrimSpace(valuePart)
	if v == "" {
		return false
	}
	// Strip trailing comment for comparison.
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	} else if i := strings.Index(v, "\t#"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	} else if strings.HasPrefix(v, "#") {
		return false
	} else if i := strings.LastIndex(v, "#"); i > 0 {
		// Bare # comment after unquoted value (no space) — rare but possible.
		// Only treat as comment if after the @ref.
		before := v[:i]
		if strings.Contains(before, "@") {
			v = strings.TrimSpace(before)
		}
	}
	v = strings.TrimSpace(v)
	// Unquote single or double quotes.
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
	}
	return v == oldUses
}

func sortChanges(c []Change) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].File != c[j].File {
			return c[i].File < c[j].File
		}
		if c[i].Line != c[j].Line {
			return c[i].Line < c[j].Line
		}
		return c[i].Action < c[j].Action
	})
}

func sortSkips(s []Skip) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].File != s[j].File {
			return s[i].File < s[j].File
		}
		if s[i].Line != s[j].Line {
			return s[i].Line < s[j].Line
		}
		return s[i].Action < s[j].Action
	})
}
