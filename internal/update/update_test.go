package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
)

// fakeLookup is an in-memory Lookup for tests.
type fakeLookup struct {
	releases map[string][]RemoteRelease // "owner/repo" → releases
	shas     map[string]string          // "owner/repo@tag" → sha
	listErr  map[string]error
	shaErr   map[string]error
}

func (f *fakeLookup) ListReleases(_ context.Context, owner, repo string) ([]RemoteRelease, error) {
	key := owner + "/" + repo
	if err := f.listErr[key]; err != nil {
		return nil, err
	}
	return f.releases[key], nil
}

func (f *fakeLookup) ResolveSHA(_ context.Context, owner, repo, ref string) (string, error) {
	key := owner + "/" + repo + "@" + ref
	if err := f.shaErr[key]; err != nil {
		return "", err
	}
	if sha, ok := f.shas[key]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("no sha for %s", key)
}

func boolPtr(b bool) *bool { return &b }

func testCatalog(minAge, prefer string) *catalog.Catalog {
	return &catalog.Catalog{
		Actions: map[string]catalog.Action{
			"actions/checkout": {
				Version:    "v4.0.0",
				SHA:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				ApprovedAt: "2026-01-01",
			},
			"actions/setup-go": {
				Version: "v5.0.0",
				SHA:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
		Policy: catalog.Policy{
			MinAge:         minAge,
			Prefer:         prefer,
			RequireComment: boolPtr(true),
		},
	}
}

func TestCheckUpdatesAvailableAndTooNew(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	old := now.Add(-14 * 24 * time.Hour)
	fresh := now.Add(-2 * 24 * time.Hour)

	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "v4.2.0", PublishedAt: old},
				{Tag: "v4.1.0", PublishedAt: old.Add(-24 * time.Hour)},
				{Tag: "latest", PublishedAt: now}, // must be ignored
			},
			"actions/setup-go": {
				{Tag: "v5.1.0", PublishedAt: fresh},
			},
		},
		shas: map[string]string{
			"actions/checkout@v4.2.0": "cccccccccccccccccccccccccccccccccccccccc",
			"actions/checkout@v4.1.0": "dddddddddddddddddddddddddddddddddddddddd",
			"actions/setup-go@v5.1.0": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		},
	}

	cat := testCatalog("7d", catalog.PreferMajor)
	result, err := CheckUpdates(context.Background(), cat, Options{
		Lookup:      lookup,
		Now:         now,
		CatalogPath: "test.yaml",
	})
	if err != nil {
		t.Fatalf("CheckUpdates: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(result.Entries))
	}

	byAction := map[string]Entry{}
	for _, e := range result.Entries {
		byAction[e.Action] = e
	}

	co := byAction["actions/checkout"]
	if co.Status != StatusAvailable {
		t.Errorf("checkout status = %q, want %s; detail=%q", co.Status, StatusAvailable, co.Detail)
	}
	if co.Eligible == nil || co.Eligible.Version != "v4.2.0" {
		t.Errorf("checkout eligible = %+v, want v4.2.0", co.Eligible)
	}
	if co.Eligible.SHA != "cccccccccccccccccccccccccccccccccccccccc" {
		t.Errorf("checkout eligible sha = %q", co.Eligible.SHA)
	}

	sg := byAction["actions/setup-go"]
	if sg.Status != StatusTooNew {
		t.Errorf("setup-go status = %q, want %s; detail=%q", sg.Status, StatusTooNew, sg.Detail)
	}
	if !result.Summary.Updates {
		t.Error("Summary.Updates = false, want true")
	}
	if result.Summary.Available != 1 || result.Summary.TooNew != 1 {
		t.Errorf("summary available=%d too-new=%d", result.Summary.Available, result.Summary.TooNew)
	}
}

func TestCheckUpdatesPreferSameMajorBlocksMajor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour)
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "v5.0.0", PublishedAt: old},
			},
			"actions/setup-go": {
				// same as catalog — current
			},
		},
		shas: map[string]string{
			"actions/checkout@v5.0.0": "ffffffffffffffffffffffffffffffffffffffff",
		},
	}
	cat := testCatalog("7d", catalog.PreferSameMajor)
	// setup-go has no newer tags
	result, err := CheckUpdates(context.Background(), cat, Options{Lookup: lookup, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var checkout Entry
	for _, e := range result.Entries {
		if e.Action == "actions/checkout" {
			checkout = e
		}
	}
	if checkout.Status != StatusBlocked {
		t.Errorf("status = %q, want %s; detail=%q", checkout.Status, StatusBlocked, checkout.Detail)
	}
	if !strings.Contains(checkout.Detail, "prefer=same-major") {
		t.Errorf("detail = %q, want prefer mention", checkout.Detail)
	}
}

func TestProposeBumpEligible(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	old := now.Add(-10 * 24 * time.Hour)
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "v4.1.0", PublishedAt: old},
			},
		},
		shas: map[string]string{
			"actions/checkout@v4.1.0": "1234567890abcdef1234567890abcdef12345678",
		},
	}
	cat := testCatalog("7d", catalog.PreferMajor)
	p, err := ProposeBump(context.Background(), cat, "checkout", Options{Lookup: lookup, Now: now})
	if err != nil {
		t.Fatalf("ProposeBump: %v", err)
	}
	if p.Action != "actions/checkout" {
		t.Errorf("Action = %q", p.Action)
	}
	if p.ToVersion != "v4.1.0" {
		t.Errorf("ToVersion = %q", p.ToVersion)
	}
	if p.ToSHA != "1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("ToSHA = %q", p.ToSHA)
	}
	if p.FromVersion != "v4.0.0" {
		t.Errorf("FromVersion = %q", p.FromVersion)
	}
	if !strings.Contains(p.Note, "not modified") {
		t.Errorf("Note = %q", p.Note)
	}
}

func TestProposeBumpRefusedWhenTooNew(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	// Released today — day 0 must never be trusted.
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "v4.9.0", PublishedAt: now},
			},
		},
		shas: map[string]string{
			"actions/checkout@v4.9.0": "9999999999999999999999999999999999999999",
		},
	}
	cat := testCatalog("7d", catalog.PreferMajor)
	_, err := ProposeBump(context.Background(), cat, "actions/checkout", Options{Lookup: lookup, Now: now})
	if err == nil {
		t.Fatal("expected refuse for day-0 release")
	}
	if !strings.Contains(err.Error(), "refused") && !strings.Contains(err.Error(), "min_age") {
		t.Errorf("error = %v, want min_age/refused", err)
	}
}

func TestProposeBumpRefusesLatestChannel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	// Only "latest" tag exists — must never be proposed.
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "latest", PublishedAt: now.Add(-30 * 24 * time.Hour)},
			},
		},
	}
	cat := testCatalog("7d", catalog.PreferMajor)
	_, err := ProposeBump(context.Background(), cat, "actions/checkout", Options{Lookup: lookup, Now: now})
	if err == nil {
		t.Fatal("expected error when only latest exists")
	}
}

func TestProposeBumpUnknownAgeWithMinAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {
				{Tag: "v4.1.0"}, // zero PublishedAt
			},
		},
		shas: map[string]string{
			"actions/checkout@v4.1.0": "1234567890abcdef1234567890abcdef12345678",
		},
	}
	cat := testCatalog("7d", catalog.PreferMajor)
	_, err := ProposeBump(context.Background(), cat, "actions/checkout", Options{Lookup: lookup, Now: now})
	if err == nil {
		t.Fatal("expected refuse when publish time unknown and min_age set")
	}
}

func TestProposeBumpAmbiguousShortName(t *testing.T) {
	t.Parallel()
	cat := &catalog.Catalog{
		Actions: map[string]catalog.Action{
			"actions/checkout": {Version: "v1.0.0", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			"other/checkout":   {Version: "v1.0.0", SHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		},
		Policy: catalog.Policy{MinAge: "1d", Prefer: catalog.PreferMajor},
	}
	_, err := ProposeBump(context.Background(), cat, "checkout", Options{
		Lookup: &fakeLookup{},
		Now:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v, want ambiguous", err)
	}
}

func TestWriteCheckAndProposal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	result := &CheckResult{
		CheckedAt: now,
		MinAge:    7 * 24 * time.Hour,
		Prefer:    catalog.PreferMajor,
		Entries: []Entry{
			{
				Action:         "actions/checkout",
				CurrentVersion: "v4.0.0",
				CurrentSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Status:         StatusAvailable,
				Latest:         &Candidate{Version: "v4.1.0", Age: 10 * 24 * time.Hour, PublishedAt: now.Add(-10 * 24 * time.Hour)},
				Eligible:       &Candidate{Version: "v4.1.0", SHA: "1234567890abcdef1234567890abcdef12345678", Age: 10 * 24 * time.Hour},
				Detail:         "v4.0.0 → v4.1.0",
			},
		},
		Summary: CheckSummary{Available: 1, Updates: true},
	}
	var buf bytes.Buffer
	if err := WriteCheck(&buf, result, FormatTable); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "available") || !strings.Contains(out, "actions/checkout") {
		t.Errorf("table = %q", out)
	}

	buf.Reset()
	if err := WriteCheck(&buf, result, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var decoded CheckResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Available != 1 {
		t.Errorf("json available = %d", decoded.Summary.Available)
	}

	p := &Proposal{
		Action:      "actions/checkout",
		FromVersion: "v4.0.0",
		FromSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ToVersion:   "v4.1.0",
		ToSHA:       "1234567890abcdef1234567890abcdef12345678",
		PublishedAt: now.Add(-10 * 24 * time.Hour),
		Age:         10 * 24 * time.Hour,
		MinAge:      7 * 24 * time.Hour,
		Prefer:      catalog.PreferMajor,
		CheckedAt:   now,
		Note:        "catalog not modified; use approve-bump to trust this pin",
	}
	buf.Reset()
	if err := WriteProposal(&buf, p, FormatTable); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "proposed bump") || !strings.Contains(buf.String(), "v4.1.0") {
		t.Errorf("proposal text = %q", buf.String())
	}
}

func TestParseActionRepo(t *testing.T) {
	t.Parallel()
	owner, repo, err := ParseActionRepo("actions/checkout")
	if err != nil || owner != "actions" || repo != "checkout" {
		t.Fatalf("got %s/%s err=%v", owner, repo, err)
	}
	owner, repo, err = ParseActionRepo("org/name/path/to/action")
	if err != nil || owner != "org" || repo != "name" {
		t.Fatalf("got %s/%s err=%v", owner, repo, err)
	}
	if _, _, err := ParseActionRepo("nopath"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAgeGateInjectableTime(t *testing.T) {
	t.Parallel()
	// Same release is too-new at T0 and available 8 days later — proves Now is used.
	published := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	lookup := &fakeLookup{
		releases: map[string][]RemoteRelease{
			"actions/checkout": {{Tag: "v4.1.0", PublishedAt: published}},
			"actions/setup-go": {},
		},
		shas: map[string]string{
			"actions/checkout@v4.1.0": "1234567890abcdef1234567890abcdef12345678",
		},
	}
	cat := testCatalog("7d", catalog.PreferMajor)

	early := published.Add(2 * 24 * time.Hour)
	_, err := ProposeBump(context.Background(), cat, "actions/checkout", Options{Lookup: lookup, Now: early})
	if err == nil {
		t.Fatal("expected refuse at day 2")
	}

	later := published.Add(8 * 24 * time.Hour)
	p, err := ProposeBump(context.Background(), cat, "actions/checkout", Options{Lookup: lookup, Now: later})
	if err != nil {
		t.Fatalf("expected allow at day 8: %v", err)
	}
	if p.ToVersion != "v4.1.0" {
		t.Errorf("ToVersion = %q", p.ToVersion)
	}
}
