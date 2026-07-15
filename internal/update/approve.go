package update

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
)

// Approval is the result of approve-bump after the catalog pin is written.
type Approval struct {
	// CatalogPath is the file that was updated.
	CatalogPath string `json:"catalogPath,omitempty"`
	// Action is the resolved catalog key.
	Action string `json:"action"`
	// FromVersion / FromSHA are the previous catalog pin.
	FromVersion string `json:"fromVersion"`
	FromSHA     string `json:"fromSha"`
	// ToVersion / ToSHA are the newly trusted pin.
	ToVersion string `json:"toVersion"`
	ToSHA     string `json:"toSha"`
	// ApprovedAt is the written approved_at date (YYYY-MM-DD).
	ApprovedAt string `json:"approvedAt"`
	// Prefer is the policy prefer value consulted for major-jump checks.
	Prefer string `json:"prefer,omitempty"`
	// MajorJump is true when major version changed.
	MajorJump bool `json:"majorJump"`
	// Source describes how the target pin was chosen: "proposal" or "explicit".
	Source string `json:"source"`
	// Note is a short human-readable summary.
	Note string `json:"note"`
}

// ApproveOptions controls approve-bump. Zero value uses ProposeBump defaults
// when Version/SHA are empty.
type ApproveOptions struct {
	// CatalogPath is required for writing (and recorded on the result).
	CatalogPath string
	// Version and SHA are an explicit pin. Both must be set, or both empty
	// (empty → resolve via ProposeBump / network lookup).
	Version string
	SHA     string
	// AllowMajor permits a major version jump when policy.prefer is
	// same-major or patch-only. When prefer is major, major jumps are always allowed.
	AllowMajor bool
	// Lookup / Now / IncludePrerelease are used when resolving a proposal.
	Lookup            Lookup
	Now               time.Time
	IncludePrerelease bool
}

func (o ApproveOptions) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now.UTC()
}

// ApproveBump writes version, sha, and approved_at for one catalog action.
//
// Modes:
//   - Explicit: Version and SHA both set → trust those values (bypasses min_age).
//   - Proposal: neither set → resolve via ProposeBump (min_age + prefer gated).
//
// Major jumps are refused when policy.prefer is same-major or patch-only unless
// AllowMajor is true. This is the only command that mutates trusted pins.
func ApproveBump(ctx context.Context, cat *catalog.Catalog, action string, opts ApproveOptions) (*Approval, error) {
	if cat == nil {
		return nil, fmt.Errorf("catalog is nil")
	}
	if strings.TrimSpace(opts.CatalogPath) == "" {
		return nil, fmt.Errorf("catalog path is required to approve a bump")
	}

	name, pin, err := resolveAction(cat, action)
	if err != nil {
		return nil, err
	}

	prefer := strings.TrimSpace(cat.Policy.Prefer)
	if prefer == "" {
		prefer = catalog.PreferMajor
	}

	toVersion, toSHA, source, err := resolveTarget(ctx, cat, name, opts)
	if err != nil {
		return nil, err
	}

	majorJump, err := checkMajorJump(pin.Version, toVersion, prefer, opts.AllowMajor)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	approvedAt := opts.now().Format("2006-01-02")
	newPin := catalog.Action{
		Version:    toVersion,
		SHA:        strings.ToLower(strings.TrimSpace(toSHA)),
		ApprovedAt: approvedAt,
	}
	if err := catalog.UpdateActionPin(opts.CatalogPath, name, newPin); err != nil {
		return nil, fmt.Errorf("write catalog pin: %w", err)
	}

	note := "catalog pin updated"
	if majorJump {
		note = "catalog pin updated (major version jump)"
	}
	return &Approval{
		CatalogPath: opts.CatalogPath,
		Action:      name,
		FromVersion: pin.Version,
		FromSHA:     pin.SHA,
		ToVersion:   newPin.Version,
		ToSHA:       newPin.SHA,
		ApprovedAt:  approvedAt,
		Prefer:      prefer,
		MajorJump:   majorJump,
		Source:      source,
		Note:        note,
	}, nil
}

func resolveTarget(
	ctx context.Context,
	cat *catalog.Catalog,
	name string,
	opts ApproveOptions,
) (version, sha, source string, err error) {
	ver := strings.TrimSpace(opts.Version)
	shaIn := strings.TrimSpace(opts.SHA)
	switch {
	case ver != "" && shaIn != "":
		if !isFullSHA(shaIn) {
			return "", "", "", fmt.Errorf("%s: sha must be a 40-character hex git commit (got %q)", name, shaIn)
		}
		return ver, strings.ToLower(shaIn), "explicit", nil
	case ver != "" || shaIn != "":
		return "", "", "", fmt.Errorf("%s: --version and --sha must be set together (or omit both to use a proposal)", name)
	default:
		// Proposal path: reuse ProposeBump (min_age + prefer). When prefer
		// blocks a major jump, callers may pass explicit --version/--sha with
		// --allow-major instead.
		proposal, perr := ProposeBump(ctx, cat, name, Options{
			CatalogPath:       opts.CatalogPath,
			Lookup:            opts.Lookup,
			Now:               opts.now(),
			IncludePrerelease: opts.IncludePrerelease,
		})
		if perr != nil {
			return "", "", "", perr
		}
		return proposal.ToVersion, proposal.ToSHA, "proposal", nil
	}
}

func checkMajorJump(fromVersion, toVersion, prefer string, allowMajor bool) (majorJump bool, err error) {
	from, errFrom := ParseVersion(fromVersion)
	to, errTo := ParseVersion(toVersion)
	if errFrom != nil || errTo != nil {
		// Non-semver pins: cannot classify major jump; allow the write.
		return false, nil
	}
	majorJump = from.Major != to.Major
	if !majorJump {
		return false, nil
	}
	// prefer=major (or empty, already normalized) always allows major jumps.
	if prefer == catalog.PreferMajor || prefer == "" {
		return true, nil
	}
	// same-major / patch-only: refuse silent major trust without --allow-major.
	if !allowMajor {
		return true, fmt.Errorf("approve-bump refused: major jump %s → %s blocked by prefer=%s (pass --allow-major to override)",
			fromVersion, toVersion, prefer)
	}
	return true, nil
}

func isFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
