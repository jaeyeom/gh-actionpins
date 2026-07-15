package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// versionRe matches common GitHub Actions tags: v1, v1.2, v1.2.3, optional -prerelease suffix.
var versionRe = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?([.-].+)?$`)

// Version is a parsed semver-like tag used for comparison and prefer filtering.
type Version struct {
	Major int
	Minor int
	Patch int
	// Pre is any pre-release / build suffix (including leading - or .), empty when none.
	Pre string
	// Raw is the original tag string.
	Raw string
}

// ParseVersion parses a tag like "v7.0.0", "v4", or "1.2.3".
// Returns an error for empty or non-version tags (e.g. "main", "latest").
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, fmt.Errorf("empty version")
	}
	// Never treat floating channels as versions we can propose.
	lower := strings.ToLower(s)
	if lower == "latest" || lower == "main" || lower == "master" || lower == "head" {
		return Version{}, fmt.Errorf("refusing non-version ref %q", s)
	}
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("not a version tag %q", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor := 0
	if m[2] != "" {
		minor, _ = strconv.Atoi(m[2])
	}
	patch := 0
	if m[3] != "" {
		patch, _ = strconv.Atoi(m[3])
	}
	return Version{
		Major: major,
		Minor: minor,
		Patch: patch,
		Pre:   m[4],
		Raw:   s,
	}, nil
}

// IsStable reports whether the version has no pre-release suffix.
func (v Version) IsStable() bool {
	return v.Pre == ""
}

// Compare returns -1 if v < other, 0 if equal, 1 if v > other.
// Pre-release versions sort before the corresponding stable release.
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		return cmpInt(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return cmpInt(v.Minor, other.Minor)
	}
	if v.Patch != other.Patch {
		return cmpInt(v.Patch, other.Patch)
	}
	// Stable (no pre) > pre-release.
	if v.Pre == "" && other.Pre != "" {
		return 1
	}
	if v.Pre != "" && other.Pre == "" {
		return -1
	}
	return strings.Compare(v.Pre, other.Pre)
}

// GreaterThan reports whether v is strictly greater than other.
func (v Version) GreaterThan(other Version) bool {
	return v.Compare(other) > 0
}

// SameMajor reports whether major versions match.
func (v Version) SameMajor(other Version) bool {
	return v.Major == other.Major
}

// SameMinor reports whether major and minor match.
func (v Version) SameMinor(other Version) bool {
	return v.Major == other.Major && v.Minor == other.Minor
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
