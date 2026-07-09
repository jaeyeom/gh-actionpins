package catalog

import (
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
