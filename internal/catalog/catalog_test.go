package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
actions:
  actions/checkout:
    version: v7.0.0
    sha: 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
    approved_at: 2026-06-01
  actions/setup-go:
    version: v6.5.0
    sha: 924ae3a1cded613372ab5595356fb5720e22ba16
    approved_at: 2026-06-01
policy:
  min_age: 7d
  prefer: major
  require_comment: true
`

func TestParseValid(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(c.Actions) != 2 {
		t.Fatalf("len(Actions) = %d, want 2", len(c.Actions))
	}
	checkout := c.Actions["actions/checkout"]
	if checkout.Version != "v7.0.0" {
		t.Errorf("checkout.Version = %q, want v7.0.0", checkout.Version)
	}
	if checkout.SHA != "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0" {
		t.Errorf("checkout.SHA = %q", checkout.SHA)
	}
	if c.Policy.MinAge != "7d" {
		t.Errorf("Policy.MinAge = %q, want 7d", c.Policy.MinAge)
	}
	if c.Policy.Prefer != PreferMajor {
		t.Errorf("Policy.Prefer = %q, want %q", c.Policy.Prefer, PreferMajor)
	}
	if !c.Policy.RequireCommentEnabled() {
		t.Error("RequireCommentEnabled() = false, want true")
	}
}

func TestParseEmptyActionsMap(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte("actions: {}\n"))
	if err != nil {
		t.Fatalf("empty actions map should be valid: %v", err)
	}
	if c.Actions == nil {
		t.Fatal("Actions is nil")
	}
}

func TestParseValidationErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name:    "missing actions",
			yaml:    "policy:\n  min_age: 1d\n",
			wantSub: "actions: required",
		},
		{
			name: "missing version",
			yaml: `
actions:
  actions/checkout:
    sha: 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
`,
			wantSub: "version is required",
		},
		{
			name: "missing sha",
			yaml: `
actions:
  actions/checkout:
    version: v7.0.0
`,
			wantSub: "sha is required",
		},
		{
			name: "short sha",
			yaml: `
actions:
  actions/checkout:
    version: v7.0.0
    sha: 9c091bb
`,
			wantSub: "40-character hex",
		},
		{
			name: "non-hex sha",
			yaml: `
actions:
  actions/checkout:
    version: v7.0.0
    sha: zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz
`,
			wantSub: "40-character hex",
		},
		{
			name: "bad approved_at",
			yaml: `
actions:
  actions/checkout:
    version: v7.0.0
    sha: 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
    approved_at: not-a-date
`,
			wantSub: "approved_at",
		},
		{
			name: "bad min_age",
			yaml: `
actions: {}
policy:
  min_age: banana
`,
			wantSub: "min_age",
		},
		{
			name: "bad prefer",
			yaml: `
actions: {}
policy:
  prefer: everything
`,
			wantSub: "policy.prefer",
		},
		{
			name:    "invalid yaml",
			yaml:    "actions: [\n",
			wantSub: "parse yaml",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatal("Parse() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "days", in: "7d", want: 7 * 24 * time.Hour},
		{name: "zero days", in: "0d", want: 0},
		{name: "hours", in: "24h", want: 24 * time.Hour},
		{name: "minutes", in: "30m", want: 30 * time.Minute},
		{name: "empty", in: "", wantErr: true},
		{name: "garbage", in: "nope", wantErr: true},
		{name: "negative days", in: "-1d", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseDuration(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoadAndDefaultPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(c.Actions) != 2 {
		t.Fatalf("len(Actions) = %d, want 2", len(c.Actions))
	}

	c2, err := LoadDefault(path)
	if err != nil {
		t.Fatalf("LoadDefault(path) error = %v", err)
	}
	if len(c2.Actions) != 2 {
		t.Fatalf("LoadDefault len = %d, want 2", len(c2.Actions))
	}

	def, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	if !strings.Contains(def, filepath.Join("actionpins", "catalog.yaml")) &&
		!strings.HasSuffix(def, "actionpins/catalog.yaml") &&
		!strings.HasSuffix(def, `actionpins\catalog.yaml`) {
		t.Errorf("DefaultPath() = %q, want .../actionpins/catalog.yaml", def)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Load(missing) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "read catalog") {
		t.Errorf("error %q, want read catalog prefix", err.Error())
	}
}

func TestRequireCommentDefault(t *testing.T) {
	t.Parallel()
	var p Policy
	if p.RequireCommentEnabled() {
		t.Error("unset RequireComment should default to false")
	}
}

func TestValidateNilCatalog(t *testing.T) {
	t.Parallel()
	var c *Catalog
	if err := c.Validate(); err == nil {
		t.Fatal("nil Catalog.Validate() = nil, want error")
	}
}

func TestParseRepos(t *testing.T) {
	t.Parallel()
	yaml := validYAML + `
repos:
  - path: /tmp/repo-a
  - name: owner/repo-b
    path: ~/src/repo-b
`
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(c.Repos) != 2 {
		t.Fatalf("len(Repos) = %d, want 2", len(c.Repos))
	}
	if c.Repos[0].Path != "/tmp/repo-a" || c.Repos[0].Name != "" {
		t.Errorf("repos[0] = %+v", c.Repos[0])
	}
	if c.Repos[1].Name != "owner/repo-b" || c.Repos[1].Path != "~/src/repo-b" {
		t.Errorf("repos[1] = %+v", c.Repos[1])
	}
}

func TestReposValidationErrors(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`
actions: {}
repos:
  - name: only-name
`))
	if err == nil {
		t.Fatal("Parse() error = nil, want path required")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("error %q, want path is required", err.Error())
	}
}

func TestResolveRepos(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, err := Parse([]byte(fmt.Sprintf(`
actions: {}
repos:
  - path: %s
  - name: acme/app
    path: %s
`, filepath.Join(dir, "a"), filepath.Join(dir, "b"))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	got, err := c.ResolveRepos()
	if err != nil {
		t.Fatalf("ResolveRepos() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if !filepath.IsAbs(got[0].Path) {
		t.Errorf("path not absolute: %q", got[0].Path)
	}
	if got[0].Label != got[0].Path {
		t.Errorf("label without name = %q, want path", got[0].Label)
	}
	if got[1].Name != "acme/app" || got[1].Label != "acme/app" {
		t.Errorf("repos[1] = %+v", got[1])
	}
}

func TestResolveReposEmpty(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte("actions: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ResolveRepos()
	if err == nil {
		t.Fatal("ResolveRepos() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no managed repositories") {
		t.Errorf("error %q", err.Error())
	}
}

func TestExpandPath(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	got, err := ExpandPath("~/actionpins-test-path")
	if err != nil {
		t.Fatalf("ExpandPath(~/...) error = %v", err)
	}
	want := filepath.Join(home, "actionpins-test-path")
	if got != want {
		t.Errorf("ExpandPath(~/...) = %q, want %q", got, want)
	}

	got, err = ExpandPath("~")
	if err != nil {
		t.Fatalf("ExpandPath(~) error = %v", err)
	}
	if got != home {
		// Abs may clean home; both should be absolute home.
		if absHome, aerr := filepath.Abs(home); aerr == nil && got != absHome {
			t.Errorf("ExpandPath(~) = %q, want %q", got, home)
		}
	}

	if _, err := ExpandPath("  "); err == nil {
		t.Error("ExpandPath(empty) = nil, want error")
	}
}
