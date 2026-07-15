// Package update discovers newer action releases, proposes catalog bumps, and
// performs the explicit approve-bump trust write. Soak policy (min_age) and
// prefer filters gate discovery so day-0 "latest" is never auto-trusted;
// only ApproveBump mutates the catalog.
package update

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
)

// Entry statuses for check-updates.
const (
	StatusCurrent   = "current"   // catalog already at newest eligible (or no newer tags)
	StatusAvailable = "available" // newer release meets prefer + min_age
	StatusTooNew    = "too-new"   // newer release exists but younger than min_age
	StatusBlocked   = "blocked"   // newer exists but filtered by prefer (e.g. major-only jump)
	StatusError     = "error"     // lookup or parse failure for this action
)

// Candidate is a proposed upstream pin (version tag + commit SHA).
type Candidate struct {
	// Version is the upstream tag (e.g. "v7.1.0").
	Version string `json:"version"`
	// SHA is the full commit SHA for Version when resolved.
	SHA string `json:"sha,omitempty"`
	// PublishedAt is when the release/tag became available (RFC3339 in JSON).
	PublishedAt time.Time `json:"publishedAt,omitempty"`
	// Age is how long since PublishedAt at evaluation time (0 if unknown).
	Age time.Duration `json:"ageNanoseconds,omitempty"`
}

// Entry is one catalog action's update check result.
type Entry struct {
	// Action is the catalog key (owner/name or owner/name/path).
	Action string `json:"action"`
	// CurrentVersion is the catalog version.
	CurrentVersion string `json:"currentVersion"`
	// CurrentSHA is the catalog commit SHA.
	CurrentSHA string `json:"currentSha"`
	// Latest is the newest version considered under prefer (may be too new).
	Latest *Candidate `json:"latest,omitempty"`
	// Eligible is the best candidate that passes min_age (when different or set).
	Eligible *Candidate `json:"eligible,omitempty"`
	// Status is current | available | too-new | blocked | error.
	Status string `json:"status"`
	// Detail is a stable human-readable reason when useful.
	Detail string `json:"detail,omitempty"`
}

// CheckResult is the outcome of check-updates.
type CheckResult struct {
	// CatalogPath is recorded when known.
	CatalogPath string `json:"catalogPath,omitempty"`
	// CheckedAt is the evaluation clock (injectable for tests).
	CheckedAt time.Time `json:"checkedAt"`
	// MinAge is the effective soak duration from policy (0 if unset).
	MinAge time.Duration `json:"minAgeNanoseconds,omitempty"`
	// Prefer is the effective prefer policy.
	Prefer string `json:"prefer,omitempty"`
	// Entries are sorted by action name.
	Entries []Entry `json:"entries"`
	// Summary counts by status.
	Summary CheckSummary `json:"summary"`
}

// CheckSummary aggregates entry statuses.
type CheckSummary struct {
	Current   int `json:"current"`
	Available int `json:"available"`
	TooNew    int `json:"tooNew"`
	Blocked   int `json:"blocked"`
	Error     int `json:"error"`
	// Updates is true when any entry is available (eligible bump exists).
	Updates bool `json:"updates"`
}

// Proposal is a reviewable propose-bump result (never writes the catalog).
type Proposal struct {
	// CatalogPath is recorded when known.
	CatalogPath string `json:"catalogPath,omitempty"`
	// Action is the resolved catalog key.
	Action string `json:"action"`
	// FromVersion / FromSHA are the current catalog pin.
	FromVersion string `json:"fromVersion"`
	FromSHA     string `json:"fromSha"`
	// ToVersion / ToSHA are the proposed pin.
	ToVersion string `json:"toVersion"`
	ToSHA     string `json:"toSha"`
	// PublishedAt is when the proposed release became available.
	PublishedAt time.Time `json:"publishedAt,omitempty"`
	// Age is soak age at evaluation time.
	Age time.Duration `json:"ageNanoseconds,omitempty"`
	// MinAge is the policy soak requirement.
	MinAge time.Duration `json:"minAgeNanoseconds,omitempty"`
	// Prefer is the policy prefer value used.
	Prefer string `json:"prefer,omitempty"`
	// CheckedAt is the evaluation clock.
	CheckedAt time.Time `json:"checkedAt"`
	// Note documents that the catalog was not modified.
	Note string `json:"note"`
}

// Options controls update checks. Zero value uses time.Now and GHLookup.
type Options struct {
	// CatalogPath is recorded on results only.
	CatalogPath string
	// Lookup fetches upstream data; defaults to GHLookup when nil.
	Lookup Lookup
	// Now is the evaluation time; defaults to time.Now when zero.
	Now time.Time
	// IncludePrerelease includes GitHub pre-releases when true.
	IncludePrerelease bool
}

func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now.UTC()
}

func (o Options) lookup() Lookup {
	if o.Lookup != nil {
		return o.Lookup
	}
	return GHLookup{}
}

// CheckUpdates compares every catalog action to upstream tags/releases.
// It never writes the catalog and never auto-trusts "latest".
func CheckUpdates(ctx context.Context, cat *catalog.Catalog, opts Options) (*CheckResult, error) {
	if cat == nil {
		return nil, fmt.Errorf("catalog is nil")
	}
	now := opts.now()
	minAge, prefer, err := policyFrom(cat)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(cat.Actions))
	for name := range cat.Actions {
		names = append(names, name)
	}
	sort.Strings(names)

	result := &CheckResult{
		CatalogPath: opts.CatalogPath,
		CheckedAt:   now,
		MinAge:      minAge,
		Prefer:      prefer,
	}
	for _, name := range names {
		entry := checkOne(ctx, name, cat.Actions[name], opts.lookup(), now, minAge, prefer, opts.IncludePrerelease)
		result.Entries = append(result.Entries, entry)
		switch entry.Status {
		case StatusCurrent:
			result.Summary.Current++
		case StatusAvailable:
			result.Summary.Available++
			result.Summary.Updates = true
		case StatusTooNew:
			result.Summary.TooNew++
		case StatusBlocked:
			result.Summary.Blocked++
		case StatusError:
			result.Summary.Error++
		}
	}
	return result, nil
}

// ProposeBump builds a reviewable bump proposal for one catalog action.
// It refuses when no eligible candidate exists (including min_age failures).
// The catalog is never modified.
func ProposeBump(ctx context.Context, cat *catalog.Catalog, action string, opts Options) (*Proposal, error) {
	if cat == nil {
		return nil, fmt.Errorf("catalog is nil")
	}
	name, pin, err := resolveAction(cat, action)
	if err != nil {
		return nil, err
	}
	now := opts.now()
	minAge, prefer, err := policyFrom(cat)
	if err != nil {
		return nil, err
	}

	entry := checkOne(ctx, name, pin, opts.lookup(), now, minAge, prefer, opts.IncludePrerelease)
	switch entry.Status {
	case StatusError:
		return nil, fmt.Errorf("%s: %s", name, entry.Detail)
	case StatusCurrent:
		return nil, fmt.Errorf("%s: no newer version under prefer=%s (current %s)", name, prefer, pin.Version)
	case StatusTooNew:
		detail := entry.Detail
		if detail == "" {
			detail = "release is younger than policy.min_age"
		}
		return nil, fmt.Errorf("%s: propose-bump refused: %s", name, detail)
	case StatusBlocked:
		detail := entry.Detail
		if detail == "" {
			detail = "newer versions excluded by policy.prefer"
		}
		return nil, fmt.Errorf("%s: propose-bump refused: %s", name, detail)
	case StatusAvailable:
		// ok
	default:
		return nil, fmt.Errorf("%s: unexpected status %q", name, entry.Status)
	}
	if entry.Eligible == nil {
		return nil, fmt.Errorf("%s: no eligible candidate", name)
	}
	cand := entry.Eligible
	if cand.SHA == "" {
		return nil, fmt.Errorf("%s: eligible version %s has no resolved SHA", name, cand.Version)
	}
	return &Proposal{
		CatalogPath: opts.CatalogPath,
		Action:      name,
		FromVersion: pin.Version,
		FromSHA:     pin.SHA,
		ToVersion:   cand.Version,
		ToSHA:       cand.SHA,
		PublishedAt: cand.PublishedAt,
		Age:         cand.Age,
		MinAge:      minAge,
		Prefer:      prefer,
		CheckedAt:   now,
		Note:        "catalog not modified; use approve-bump to trust this pin",
	}, nil
}

func policyFrom(cat *catalog.Catalog) (minAge time.Duration, prefer string, err error) {
	prefer = strings.TrimSpace(cat.Policy.Prefer)
	if prefer == "" {
		prefer = catalog.PreferMajor
	}
	if cat.Policy.MinAge != "" {
		minAge, err = catalog.ParseDuration(cat.Policy.MinAge)
		if err != nil {
			return 0, "", fmt.Errorf("policy.min_age: %w", err)
		}
	}
	return minAge, prefer, nil
}

func resolveAction(cat *catalog.Catalog, action string) (string, catalog.Action, error) {
	action = strings.TrimSpace(action)
	if action == "" {
		return "", catalog.Action{}, fmt.Errorf("action name is required")
	}
	if pin, ok := cat.Actions[action]; ok {
		return action, pin, nil
	}
	// Unique suffix / basename match (e.g. "checkout" → "actions/checkout").
	var matches []string
	for name := range cat.Actions {
		if name == action || strings.HasSuffix(name, "/"+action) {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", catalog.Action{}, fmt.Errorf("action %q not in catalog", action)
	case 1:
		return matches[0], cat.Actions[matches[0]], nil
	default:
		return "", catalog.Action{}, fmt.Errorf("action %q is ambiguous: %s", action, strings.Join(matches, ", "))
	}
}

// scored is an internal ranking of one upstream release against catalog policy.
type scored struct {
	rel   RemoteRelease
	ver   Version
	age   time.Duration
	okAge bool
}

func checkOne(
	ctx context.Context,
	action string,
	pin catalog.Action,
	lookup Lookup,
	now time.Time,
	minAge time.Duration,
	prefer string,
	includePrerelease bool,
) Entry {
	entry := Entry{
		Action:         action,
		CurrentVersion: pin.Version,
		CurrentSHA:     pin.SHA,
	}
	current, err := ParseVersion(pin.Version)
	if err != nil {
		entry.Status = StatusError
		entry.Detail = fmt.Sprintf("catalog version: %v", err)
		return entry
	}
	owner, repo, err := ParseActionRepo(action)
	if err != nil {
		entry.Status = StatusError
		entry.Detail = err.Error()
		return entry
	}
	releases, err := lookup.ListReleases(ctx, owner, repo)
	if err != nil {
		entry.Status = StatusError
		entry.Detail = err.Error()
		return entry
	}

	newer, newerAny := collectNewer(releases, current, now, minAge, prefer, includePrerelease)
	if len(newerAny) == 0 {
		entry.Status = StatusCurrent
		entry.Detail = "up to date"
		return entry
	}
	sortNewest(newer)
	sortNewest(newerAny)

	if len(newer) == 0 {
		top := newerAny[0]
		entry.Latest = candidateFrom(top, "")
		entry.Status = StatusBlocked
		entry.Detail = fmt.Sprintf("newer %s excluded by prefer=%s", top.ver.Raw, prefer)
		return entry
	}
	return finishEligible(ctx, entry, pin, lookup, owner, repo, newer, minAge)
}

func collectNewer(
	releases []RemoteRelease,
	current Version,
	now time.Time,
	minAge time.Duration,
	prefer string,
	includePrerelease bool,
) (newer, newerAny []scored) {
	for _, rel := range releases {
		s, ok := scoreRelease(rel, current, now, minAge, includePrerelease)
		if !ok {
			continue
		}
		newerAny = append(newerAny, s)
		if matchesPrefer(prefer, current, s.ver) {
			newer = append(newer, s)
		}
	}
	return newer, newerAny
}

func scoreRelease(
	rel RemoteRelease,
	current Version,
	now time.Time,
	minAge time.Duration,
	includePrerelease bool,
) (scored, bool) {
	if rel.Draft {
		return scored{}, false
	}
	if rel.Prerelease && !includePrerelease {
		return scored{}, false
	}
	ver, err := ParseVersion(rel.Tag)
	if err != nil {
		return scored{}, false // skip latest/main/non-semver
	}
	if !includePrerelease && !ver.IsStable() {
		return scored{}, false
	}
	if !ver.GreaterThan(current) {
		return scored{}, false
	}
	s := scored{rel: rel, ver: ver}
	switch {
	case !rel.PublishedAt.IsZero():
		s.age = now.Sub(rel.PublishedAt)
		if s.age < 0 {
			s.age = 0
		}
		s.okAge = minAge == 0 || s.age >= minAge
	case minAge == 0:
		// Unknown age: only eligible when soak is disabled.
		s.okAge = true
	default:
		// Unknown publish time with soak enabled: treat as too new (safe).
		s.okAge = false
	}
	return s, true
}

func sortNewest(items []scored) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].ver.Compare(items[j].ver) > 0
	})
}

func finishEligible(
	ctx context.Context,
	entry Entry,
	pin catalog.Action,
	lookup Lookup,
	owner, repo string,
	newer []scored,
	minAge time.Duration,
) Entry {
	top := newer[0]
	sha, _ := resolveCandidateSHA(ctx, lookup, owner, repo, top.rel)
	entry.Latest = candidateFrom(top, sha)

	eligible := firstEligible(newer)
	if eligible == nil {
		entry.Status = StatusTooNew
		entry.Detail = tooNewDetail(top, minAge)
		return entry
	}

	eligSHA := sha
	if eligible.ver.Raw != top.ver.Raw {
		var err error
		eligSHA, err = resolveCandidateSHA(ctx, lookup, owner, repo, eligible.rel)
		if err != nil {
			entry.Status = StatusError
			entry.Detail = err.Error()
			return entry
		}
	}
	if eligSHA == "" {
		var err error
		eligSHA, err = resolveCandidateSHA(ctx, lookup, owner, repo, eligible.rel)
		if err != nil {
			entry.Status = StatusError
			entry.Detail = err.Error()
			return entry
		}
	}
	entry.Eligible = candidateFrom(*eligible, eligSHA)
	if entry.Latest != nil && entry.Latest.Version == entry.Eligible.Version {
		entry.Latest.SHA = eligSHA
	}
	entry.Status = StatusAvailable
	entry.Detail = fmt.Sprintf("%s → %s", pin.Version, eligible.ver.Raw)
	return entry
}

func firstEligible(newer []scored) *scored {
	for i := range newer {
		if newer[i].okAge {
			return &newer[i]
		}
	}
	return nil
}

func tooNewDetail(top scored, minAge time.Duration) string {
	if top.rel.PublishedAt.IsZero() {
		return fmt.Sprintf("%s has unknown publish time; cannot satisfy min_age=%s", top.ver.Raw, formatDuration(minAge))
	}
	return fmt.Sprintf("%s age %s < min_age %s", top.ver.Raw, formatAge(top.age), formatDuration(minAge))
}

func resolveCandidateSHA(ctx context.Context, lookup Lookup, owner, repo string, rel RemoteRelease) (string, error) {
	if rel.SHA != "" && len(rel.SHA) == 40 {
		return strings.ToLower(rel.SHA), nil
	}
	sha, err := lookup.ResolveSHA(ctx, owner, repo, rel.Tag)
	if err != nil {
		return "", fmt.Errorf("resolve %s/%s@%s: %w", owner, repo, rel.Tag, err)
	}
	return sha, nil
}

func candidateFrom(s scored, sha string) *Candidate {
	c := &Candidate{
		Version:     s.ver.Raw,
		SHA:         sha,
		PublishedAt: s.rel.PublishedAt,
		Age:         s.age,
	}
	return c
}

func matchesPrefer(prefer string, current, cand Version) bool {
	switch prefer {
	case catalog.PreferPatchOnly:
		return cand.SameMinor(current)
	case catalog.PreferSameMajor:
		return cand.SameMajor(current)
	case catalog.PreferMajor, "":
		return true
	default:
		// Unknown prefer should have been rejected at catalog load; allow all.
		return true
	}
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return d.String()
}

func formatAge(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	days := int(d / (24 * time.Hour))
	if days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d / time.Hour)
	if hours >= 1 {
		return fmt.Sprintf("%dh", hours)
	}
	return d.Round(time.Second).String()
}
