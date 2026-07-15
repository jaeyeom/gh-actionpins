// Package catalog loads and validates the trusted action pin catalog.
//
// The catalog is the source of truth for approved GitHub Actions versions
// (commit SHAs). Other commands load it via [Load] or [LoadDefault].
package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultRelPath is the path under the user config directory for the catalog.
const DefaultRelPath = "actionpins/catalog.yaml"

// Prefer values accepted in policy.prefer.
const (
	PreferMajor     = "major"
	PreferSameMajor = "same-major"
	PreferPatchOnly = "patch-only"
)

// Catalog is the trusted pin registry loaded from YAML.
//
// Optional Repos lists managed local checkouts for fleet commands
// (scan/diff/apply --all). Inventory remains discovery-based per repo:
// unused catalog actions are never injected into a repo.
type Catalog struct {
	Actions map[string]Action `yaml:"actions"`
	Policy  Policy            `yaml:"policy"`
	// Repos is the managed fleet list (local paths). Optional.
	Repos []Repo `yaml:"repos,omitempty"`
}

// Repo is one managed local checkout in the fleet.
//
// Path is required and points at a local working tree. Name is optional
// owner/name identity used only for display (not for network access).
type Repo struct {
	// Name is an optional owner/name label (e.g. "jaeyeom/gh-actionpins").
	Name string `yaml:"name,omitempty"`
	// Path is the local filesystem path to the checkout (required).
	// Supports leading "~/" for the user home directory.
	Path string `yaml:"path"`
}

// ResolvedRepo is a validated managed repo with an absolute local path.
type ResolvedRepo struct {
	// Name is the optional owner/name identity (may be empty).
	Name string
	// Path is the absolute local path.
	Path string
	// Label is Name when set, otherwise Path (for human-readable headers).
	Label string
}

// Action is one trusted pin entry (version tag + immutable commit SHA).
type Action struct {
	Version    string `yaml:"version"`
	SHA        string `yaml:"sha"`
	ApprovedAt string `yaml:"approved_at"`
}

// Policy holds bump/apply preferences. Fields are validated when set.
// require_comment is honored by diff and apply; min_age/prefer are for bumps.
type Policy struct {
	MinAge         string `yaml:"min_age"`
	Prefer         string `yaml:"prefer"`
	RequireComment *bool  `yaml:"require_comment"`
}

// shaFull matches a full 40-character lowercase or uppercase git commit SHA.
var shaFull = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)

// Load reads and validates a catalog from path.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog %s: %w", path, err)
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", path, err)
	}
	return c, nil
}

// LoadDefault loads the catalog from DefaultPath (or path if non-empty).
func LoadDefault(path string) (*Catalog, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	return Load(path)
}

// DefaultPath returns ~/.config/actionpins/catalog.yaml (or OS equivalent).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, DefaultRelPath), nil
}

// Parse unmarshals YAML bytes and validates the catalog.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks required fields and field shapes.
func (c *Catalog) Validate() error {
	if c == nil {
		return errors.New("catalog is nil")
	}
	if c.Actions == nil {
		return errors.New("actions: required map is missing")
	}

	var errs []error
	for name, action := range c.Actions {
		if name == "" {
			errs = append(errs, errors.New("actions: empty action name"))
			continue
		}
		if err := validateAction(name, action); err != nil {
			errs = append(errs, err)
		}
	}
	if err := c.Policy.validate(); err != nil {
		errs = append(errs, err)
	}
	for i, r := range c.Repos {
		if err := validateRepo(i, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ResolveRepos expands and absolutizes managed repo paths.
// Returns an error when the fleet list is empty (needed for --all).
// Does not require paths to exist on disk; callers should check as needed.
func (c *Catalog) ResolveRepos() ([]ResolvedRepo, error) {
	if c == nil {
		return nil, errors.New("catalog is nil")
	}
	if len(c.Repos) == 0 {
		return nil, errors.New("repos: no managed repositories configured (add a repos: list to the catalog for --all)")
	}
	out := make([]ResolvedRepo, 0, len(c.Repos))
	for i, r := range c.Repos {
		if err := validateRepo(i, r); err != nil {
			return nil, err
		}
		abs, err := ExpandPath(r.Path)
		if err != nil {
			return nil, fmt.Errorf("repos[%d]: path: %w", i, err)
		}
		name := strings.TrimSpace(r.Name)
		label := name
		if label == "" {
			label = abs
		}
		out = append(out, ResolvedRepo{Name: name, Path: abs, Label: label})
	}
	return out, nil
}

// ExpandPath trims p, expands a leading "~/" (or bare "~") to the user home
// directory, and returns an absolute path.
func ExpandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			// Drop "~/" or "~\"
			p = filepath.Join(home, p[2:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	return abs, nil
}

func validateRepo(i int, r Repo) error {
	if strings.TrimSpace(r.Path) == "" {
		return fmt.Errorf("repos[%d]: path is required", i)
	}
	return nil
}

func validateAction(name string, a Action) error {
	var errs []error
	if strings.TrimSpace(a.Version) == "" {
		errs = append(errs, fmt.Errorf("actions[%s]: version is required", name))
	}
	if strings.TrimSpace(a.SHA) == "" {
		errs = append(errs, fmt.Errorf("actions[%s]: sha is required", name))
	} else if !shaFull.MatchString(a.SHA) {
		errs = append(errs, fmt.Errorf("actions[%s]: sha must be a 40-character hex git commit (got %q)", name, a.SHA))
	}
	if a.ApprovedAt != "" {
		if _, err := parseDate(a.ApprovedAt); err != nil {
			errs = append(errs, fmt.Errorf("actions[%s]: approved_at: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (p Policy) validate() error {
	var errs []error
	if p.MinAge != "" {
		if _, err := ParseDuration(p.MinAge); err != nil {
			errs = append(errs, fmt.Errorf("policy.min_age: %w", err))
		}
	}
	if p.Prefer != "" {
		switch p.Prefer {
		case PreferMajor, PreferSameMajor, PreferPatchOnly:
		default:
			errs = append(errs, fmt.Errorf("policy.prefer: must be one of %q, %q, %q (got %q)",
				PreferMajor, PreferSameMajor, PreferPatchOnly, p.Prefer))
		}
	}
	return errors.Join(errs...)
}

// ParseDuration parses durations like "7d", "24h", "30m", or Go durations.
// Day units ("d") are accepted as 24h multiples; bare numbers are rejected.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	// Support Nd (days) which time.ParseDuration does not.
	if strings.HasSuffix(s, "d") && len(s) > 1 {
		daysPart := strings.TrimSuffix(s, "d")
		// Reject composites like "1h2d"; only pure day form here.
		if !strings.ContainsAny(daysPart, "hmsuµn") {
			days, err := strconv.Atoi(daysPart)
			if err != nil {
				return 0, fmt.Errorf("invalid day duration %q", s)
			}
			if days < 0 {
				return 0, fmt.Errorf("negative duration %q", s)
			}
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration %q", s)
	}
	return d, nil
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("must be YYYY-MM-DD (got %q)", s)
}

// RequireComment returns whether version comments are required on pin lines.
// Defaults to false when unset.
func (p Policy) RequireCommentEnabled() bool {
	if p.RequireComment == nil {
		return false
	}
	return *p.RequireComment
}
