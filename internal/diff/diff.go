// Package diff compares scanned workflow action refs against the trusted catalog.
//
// Each finding is classified as ok, mismatch, unpinned, or unknown so CI and
// humans can see drift without rewriting files (that is apply's job).
package diff

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
	"github.com/jaeyeom/gh-actionpins/internal/scan"
)

// Status is the drift classification for one uses: reference.
type Status string

const (
	// StatusOK means the ref matches the catalog SHA (and version comment when required).
	StatusOK Status = "ok"
	// StatusMismatch means a different SHA than the catalog (or SHA match but bad comment policy).
	StatusMismatch Status = "mismatch"
	// StatusUnpinned means a floating tag/branch (not a full commit SHA) for a catalogued action.
	StatusUnpinned Status = "unpinned"
	// StatusUnknown means the action is not present in the catalog.
	StatusUnknown Status = "unknown"
)

// shaFull matches a full 40-character git commit SHA (any hex case).
var shaFull = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)

// Entry is one scan finding with its catalog comparison result.
type Entry struct {
	// File is the path relative to the scan root (slash-separated).
	File string `json:"file"`
	// Line is the 1-based line of the uses value in the source file.
	Line int `json:"line"`
	// Action is owner/name or owner/name/path (everything before @).
	Action string `json:"action"`
	// Ref is the pin after @ as written in the workflow.
	Ref string `json:"ref"`
	// Uses is the full uses string as written (action@ref).
	Uses string `json:"uses"`
	// Status is the classification against the catalog.
	Status Status `json:"status"`
	// CatalogVersion is the trusted version tag when the action is catalogued.
	CatalogVersion string `json:"catalogVersion,omitempty"`
	// CatalogSHA is the trusted commit SHA when the action is catalogued.
	CatalogSHA string `json:"catalogSha,omitempty"`
	// Detail is an optional human-readable reason (stable for tests when set).
	Detail string `json:"detail,omitempty"`
}

// Summary counts entries by status.
type Summary struct {
	OK       int `json:"ok"`
	Mismatch int `json:"mismatch"`
	Unpinned int `json:"unpinned"`
	Unknown  int `json:"unknown"`
	// Drift is true when any entry is not ok.
	Drift bool `json:"drift"`
}

// Result is the outcome of comparing a scan against a catalog.
type Result struct {
	// Root is the absolute path that was scanned.
	Root string `json:"root"`
	// CatalogPath is the catalog file path when known (may be empty).
	CatalogPath string `json:"catalogPath,omitempty"`
	// Entries lists every scan finding with classification, in scan order stability.
	Entries []Entry `json:"entries"`
	// Summary aggregates status counts.
	Summary Summary `json:"summary"`
}

// Options controls comparison behavior. Zero value uses catalog policy.
type Options struct {
	// CatalogPath is recorded on the result only (not used for loading).
	CatalogPath string
	// EnforceComment, when non-nil, overrides catalog policy.require_comment.
	EnforceComment *bool
}

// Compare classifies each scan finding against cat.
// scanResult may be empty; a nil catalog is an error.
func Compare(cat *catalog.Catalog, scanResult *scan.Result, opts Options) (*Result, error) {
	if cat == nil {
		return nil, fmt.Errorf("catalog is nil")
	}
	if scanResult == nil {
		return nil, fmt.Errorf("scan result is nil")
	}

	requireComment := cat.Policy.RequireCommentEnabled()
	if opts.EnforceComment != nil {
		requireComment = *opts.EnforceComment
	}

	res := &Result{
		Root:        scanResult.Root,
		CatalogPath: opts.CatalogPath,
		Entries:     make([]Entry, 0, len(scanResult.Findings)),
	}

	for _, f := range scanResult.Findings {
		res.Entries = append(res.Entries, classify(cat, scanResult.Root, f, requireComment))
	}
	sortEntries(res.Entries)
	res.Summary = summarize(res.Entries)
	return res, nil
}

// HasDrift reports whether any entry is not ok.
func (r *Result) HasDrift() bool {
	if r == nil {
		return false
	}
	return r.Summary.Drift
}

func classify(cat *catalog.Catalog, root string, f scan.Finding, requireComment bool) Entry {
	e := Entry{
		File:   f.File,
		Line:   f.Line,
		Action: f.Action,
		Ref:    f.Ref,
		Uses:   f.Uses,
	}

	action, ok := cat.Actions[f.Action]
	if !ok {
		e.Status = StatusUnknown
		e.Detail = "action not in catalog"
		return e
	}
	e.CatalogVersion = action.Version
	e.CatalogSHA = action.SHA

	if !isFullSHA(f.Ref) {
		e.Status = StatusUnpinned
		e.Detail = "ref is not a full commit SHA"
		return e
	}

	if !shaEqual(f.Ref, action.SHA) {
		e.Status = StatusMismatch
		e.Detail = "SHA differs from catalog"
		return e
	}

	if requireComment {
		comment, err := versionComment(root, f.File, f.Line)
		if err != nil {
			e.Status = StatusMismatch
			e.Detail = "could not read version comment: " + err.Error()
			return e
		}
		want := normalizeVersion(action.Version)
		got := normalizeVersion(comment)
		if got == "" {
			e.Status = StatusMismatch
			e.Detail = "missing version comment (policy.require_comment)"
			return e
		}
		if got != want {
			e.Status = StatusMismatch
			e.Detail = fmt.Sprintf("version comment %q does not match catalog version %q", comment, action.Version)
			return e
		}
	}

	e.Status = StatusOK
	return e
}

func isFullSHA(ref string) bool {
	return shaFull.MatchString(strings.TrimSpace(ref))
}

func shaEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// versionComment returns the trailing # comment text on the uses line (without #).
func versionComment(root, relFile string, line int) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("invalid line %d", line)
	}
	path := filepath.Join(root, filepath.FromSlash(relFile))
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", relFile, err)
	}
	defer func() { _ = file.Close() }()

	sc := bufio.NewScanner(file)
	// Allow long workflow lines.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	cur := 0
	for sc.Scan() {
		cur++
		if cur == line {
			return parseTrailingComment(sc.Text()), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("scan %s: %w", relFile, err)
	}
	return "", fmt.Errorf("line %d not found in %s", line, relFile)
}

func parseTrailingComment(line string) string {
	// Prefer the last " #" so values with # in strings are less likely to break;
	// uses: lines are typically scalar + optional comment.
	idx := strings.LastIndex(line, "#")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[idx+1:])
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "#")
	return strings.TrimSpace(v)
}

func summarize(entries []Entry) Summary {
	var s Summary
	for _, e := range entries {
		switch e.Status {
		case StatusOK:
			s.OK++
		case StatusMismatch:
			s.Mismatch++
			s.Drift = true
		case StatusUnpinned:
			s.Unpinned++
			s.Drift = true
		case StatusUnknown:
			s.Unknown++
			s.Drift = true
		default:
			s.Drift = true
		}
	}
	return s
}

func sortEntries(e []Entry) {
	sort.Slice(e, func(i, j int) bool {
		if e[i].File != e[j].File {
			return e[i].File < e[j].File
		}
		if e[i].Line != e[j].Line {
			return e[i].Line < e[j].Line
		}
		if e[i].Action != e[j].Action {
			return e[i].Action < e[j].Action
		}
		return e[i].Ref < e[j].Ref
	})
}
