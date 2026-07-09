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
type Catalog struct {
	Actions map[string]Action `yaml:"actions"`
	Policy  Policy            `yaml:"policy"`
}

// Action is one trusted pin entry (version tag + immutable commit SHA).
type Action struct {
	Version    string `yaml:"version"`
	SHA        string `yaml:"sha"`
	ApprovedAt string `yaml:"approved_at"`
}

// Policy holds bump/apply preferences. Fields are validated when set;
// behavior for each field may be partial until bump/apply land.
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
	return errors.Join(errs...)
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
