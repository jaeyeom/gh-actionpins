// Command gh-actionpins is a GitHub CLI extension that manages trusted
// GitHub Actions pins across a personal or org repo fleet.
//
// Install: gh extension install jaeyeom/gh-actionpins
// Run:     gh actionpins --help
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jaeyeom/gh-actionpins/internal/apply"
	"github.com/jaeyeom/gh-actionpins/internal/catalog"
	"github.com/jaeyeom/gh-actionpins/internal/diff"
	"github.com/jaeyeom/gh-actionpins/internal/scan"
)

const usage = `gh-actionpins manages trusted GitHub Actions pins.

A catalog of approved action versions (commit SHAs) is the source of truth.
Scan, diff, and apply pin those versions across managed repositories; bumps
require an explicit approve path with soak time.

Usage:
  gh actionpins <command> [flags]

Commands:
  catalog validate    Validate a catalog YAML file
  scan [path]         List action uses: from local workflows
  diff [path]         Compare workflow refs to the trusted catalog
  apply [path]        Rewrite uses: to catalog SHA + version comment
  help                Show this help

Flags:
  -h, --help    Show this help

Future commands (see repo issues):
  check-updates, propose-bump, approve-bump

Catalog:
  Default path: ~/.config/actionpins/catalog.yaml (OS user config dir)
  Example:      examples/catalog.yaml

  Optional repos: list for fleet operations (scan/diff/apply --all):
    repos:
      - path: /local/checkout          # path required
      - name: owner/repo               # optional display identity
        path: ~/src/owner/repo         # ~/ expands to home

  Inventory is always discovery-based per repo: unused catalog actions
  are never injected. Profiles are not required for --all.

Scan:
  Walks .github/workflows/** for owner/name@ref (and owner/name/path@ref).
  Local (./...) and Docker (docker://...) uses are skipped.
  --all iterates catalog.repos (requires a catalog with a non-empty list).
  Output: table (default) or JSON (--format).

Diff:
  Loads the catalog, scans [path] (default: .), classifies each uses: as
  ok | mismatch | unpinned | unknown. policy.require_comment is enforced
  when set in the catalog.
  --all iterates catalog.repos.
  Exit codes:
    0  no drift (every finding is ok, or no findings)
    1  drift present, or catalog/scan failure
    2  invalid usage/flags
  Output: table (default) or JSON (--format).

Apply:
  Rewrites mismatched/unpinned catalogued actions to:
    owner/action@<sha> # <version>   (when policy.require_comment)
    owner/action@<sha>               (when require_comment is false)
  Unknown, local, and Docker uses are left unchanged (reported as skipped).
  Local file updates only; use --dry-run to preview without writing.
  --all iterates catalog.repos (still discovery-based per repo).
  Output: table (default) or JSON (--format).

Examples:
  gh actionpins --help
  gh actionpins catalog validate
  gh actionpins catalog validate --catalog examples/catalog.yaml
  gh actionpins scan
  gh actionpins scan --format json
  gh actionpins scan --format json /path/to/repo
  gh actionpins scan --all --catalog examples/catalog.yaml
  gh actionpins diff
  gh actionpins diff --catalog examples/catalog.yaml
  gh actionpins diff --format json /path/to/repo
  gh actionpins diff --all --catalog examples/catalog.yaml
  gh actionpins apply --dry-run
  gh actionpins apply --catalog examples/catalog.yaml
  gh actionpins apply --dry-run --format json /path/to/repo
  gh actionpins apply --all --dry-run --catalog examples/catalog.yaml
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "catalog":
		return runCatalog(args[1:], stdout, stderr)
	case "scan":
		return runScan(args[1:], stdout, stderr)
	case "diff":
		return runDiff(args[1:], stdout, stderr)
	case "apply":
		return runApply(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 1
	}
}

func runScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", scan.FormatTable, "output format: table or json")
	all := fs.Bool("all", false, "scan every repo listed in catalog.repos")
	catalogPath := fs.String("catalog", "", "path to catalog YAML (required for --all; default: user config actionpins/catalog.yaml)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *all {
		if fs.NArg() != 0 {
			_, _ = fmt.Fprintln(stderr, "usage: gh actionpins scan --all [--catalog path] [--format table|json]")
			return 2
		}
		return runScanAll(*catalogPath, *format, stdout, stderr)
	}

	root := "."
	switch fs.NArg() {
	case 0:
		// default: current directory
	case 1:
		root = fs.Arg(0)
	default:
		_, _ = fmt.Fprintln(stderr, "usage: gh actionpins scan [path] [--format table|json]")
		return 2
	}

	result, err := scan.Scan(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := scan.Write(stdout, result, *format); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runScanAll(catalogPath, format string, stdout, stderr io.Writer) int {
	repos, err := loadManagedRepos(catalogPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	type repoScan struct {
		Name     string         `json:"name,omitempty"`
		Path     string         `json:"path"`
		Root     string         `json:"root"`
		Findings []scan.Finding `json:"findings"`
		Error    string         `json:"error,omitempty"`
	}
	type multiResult struct {
		Repos []repoScan `json:"repos"`
	}

	isJSON := isJSONFormat(format)
	var multi multiResult
	exit := 0

	for i, repo := range repos {
		result, scanErr := scan.Scan(repo.Path)
		if scanErr != nil {
			exit = 1
			_, _ = fmt.Fprintf(stderr, "error: %s: %v\n", repo.Label, scanErr)
			if isJSON {
				multi.Repos = append(multi.Repos, repoScan{
					Name:  repo.Name,
					Path:  repo.Path,
					Error: scanErr.Error(),
				})
			}
			continue
		}
		if isJSON {
			multi.Repos = append(multi.Repos, repoScan{
				Name:     repo.Name,
				Path:     repo.Path,
				Root:     result.Root,
				Findings: result.Findings,
			})
			continue
		}
		if err := writeRepoHeader(stdout, repo, i > 0); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if err := scan.Write(stdout, result, format); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}

	if isJSON {
		if err := writeJSON(stdout, multi); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	return exit
}

func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog YAML (default: user config actionpins/catalog.yaml)")
	format := fs.String("format", diff.FormatTable, "output format: table or json")
	all := fs.Bool("all", false, "diff every repo listed in catalog.repos")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *all {
		if fs.NArg() != 0 {
			_, _ = fmt.Fprintln(stderr, "usage: gh actionpins diff --all [--catalog path] [--format table|json]")
			return 2
		}
		return runDiffAll(*catalogPath, *format, stdout, stderr)
	}

	root := "."
	switch fs.NArg() {
	case 0:
		// default: current directory
	case 1:
		root = fs.Arg(0)
	default:
		_, _ = fmt.Fprintln(stderr, "usage: gh actionpins diff [path] [--catalog path] [--format table|json]")
		return 2
	}

	path, cat, code := loadCatalog(*catalogPath, stderr)
	if code != 0 {
		return code
	}

	scanResult, err := scan.Scan(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	result, err := diff.Compare(cat, scanResult, diff.Options{CatalogPath: path})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := diff.Write(stdout, result, *format); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if result.HasDrift() {
		return 1
	}
	return 0
}

func runDiffAll(catalogPath, format string, stdout, stderr io.Writer) int {
	path, cat, code := loadCatalog(catalogPath, stderr)
	if code != 0 {
		return code
	}
	repos, err := cat.ResolveRepos()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	type repoDiff struct {
		Name    string       `json:"name,omitempty"`
		Path    string       `json:"path"`
		Root    string       `json:"root,omitempty"`
		Entries []diff.Entry `json:"entries,omitempty"`
		Summary diff.Summary `json:"summary,omitempty"`
		Error   string       `json:"error,omitempty"`
	}
	type multiResult struct {
		CatalogPath string     `json:"catalogPath,omitempty"`
		Repos       []repoDiff `json:"repos"`
		// Drift is true when any repo has drift or an error.
		Drift bool `json:"drift"`
	}

	isJSON := isJSONFormat(format)
	multi := multiResult{CatalogPath: path}
	exit := 0

	for i, repo := range repos {
		scanResult, scanErr := scan.Scan(repo.Path)
		if scanErr != nil {
			exit = 1
			multi.Drift = true
			_, _ = fmt.Fprintf(stderr, "error: %s: %v\n", repo.Label, scanErr)
			if isJSON {
				multi.Repos = append(multi.Repos, repoDiff{
					Name:  repo.Name,
					Path:  repo.Path,
					Error: scanErr.Error(),
				})
			}
			continue
		}
		result, cmpErr := diff.Compare(cat, scanResult, diff.Options{CatalogPath: path})
		if cmpErr != nil {
			exit = 1
			multi.Drift = true
			_, _ = fmt.Fprintf(stderr, "error: %s: %v\n", repo.Label, cmpErr)
			if isJSON {
				multi.Repos = append(multi.Repos, repoDiff{
					Name:  repo.Name,
					Path:  repo.Path,
					Error: cmpErr.Error(),
				})
			}
			continue
		}
		if result.HasDrift() {
			exit = 1
			multi.Drift = true
		}
		if isJSON {
			multi.Repos = append(multi.Repos, repoDiff{
				Name:    repo.Name,
				Path:    repo.Path,
				Root:    result.Root,
				Entries: result.Entries,
				Summary: result.Summary,
			})
			continue
		}
		if err := writeRepoHeader(stdout, repo, i > 0); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if err := diff.Write(stdout, result, format); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}

	if isJSON {
		if err := writeJSON(stdout, multi); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	return exit
}

func runApply(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog YAML (default: user config actionpins/catalog.yaml)")
	format := fs.String("format", apply.FormatTable, "output format: table or json")
	dryRun := fs.Bool("dry-run", false, "show planned changes without writing files")
	all := fs.Bool("all", false, "apply to every repo listed in catalog.repos")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *all {
		if fs.NArg() != 0 {
			_, _ = fmt.Fprintln(stderr, "usage: gh actionpins apply --all [--catalog path] [--dry-run] [--format table|json]")
			return 2
		}
		return runApplyAll(*catalogPath, *format, *dryRun, stdout, stderr)
	}

	root := "."
	switch fs.NArg() {
	case 0:
		// default: current directory
	case 1:
		root = fs.Arg(0)
	default:
		_, _ = fmt.Fprintln(stderr, "usage: gh actionpins apply [path] [--catalog path] [--dry-run] [--format table|json]")
		return 2
	}

	path, cat, code := loadCatalog(*catalogPath, stderr)
	if code != 0 {
		return code
	}

	result, err := apply.Run(cat, root, apply.Options{
		CatalogPath: path,
		DryRun:      *dryRun,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := apply.Write(stdout, result, *format); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runApplyAll(catalogPath, format string, dryRun bool, stdout, stderr io.Writer) int {
	path, cat, code := loadCatalog(catalogPath, stderr)
	if code != 0 {
		return code
	}
	repos, err := cat.ResolveRepos()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	type repoApply struct {
		Name    string         `json:"name,omitempty"`
		Path    string         `json:"path"`
		Root    string         `json:"root,omitempty"`
		DryRun  bool           `json:"dryRun"`
		Changes []apply.Change `json:"changes,omitempty"`
		Skipped []apply.Skip   `json:"skipped,omitempty"`
		Error   string         `json:"error,omitempty"`
	}
	type multiResult struct {
		CatalogPath string      `json:"catalogPath,omitempty"`
		DryRun      bool        `json:"dryRun"`
		Repos       []repoApply `json:"repos"`
	}

	isJSON := isJSONFormat(format)
	multi := multiResult{CatalogPath: path, DryRun: dryRun}
	exit := 0

	for i, repo := range repos {
		result, runErr := apply.Run(cat, repo.Path, apply.Options{
			CatalogPath: path,
			DryRun:      dryRun,
		})
		if runErr != nil {
			exit = 1
			_, _ = fmt.Fprintf(stderr, "error: %s: %v\n", repo.Label, runErr)
			if isJSON {
				multi.Repos = append(multi.Repos, repoApply{
					Name:   repo.Name,
					Path:   repo.Path,
					DryRun: dryRun,
					Error:  runErr.Error(),
				})
			}
			continue
		}
		if isJSON {
			multi.Repos = append(multi.Repos, repoApply{
				Name:    repo.Name,
				Path:    repo.Path,
				Root:    result.Root,
				DryRun:  result.DryRun,
				Changes: result.Changes,
				Skipped: result.Skipped,
			})
			continue
		}
		if err := writeRepoHeader(stdout, repo, i > 0); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if err := apply.Write(stdout, result, format); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}

	if isJSON {
		if err := writeJSON(stdout, multi); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	return exit
}

func runCatalog(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, `usage: gh actionpins catalog <subcommand>

Subcommands:
  validate    Load and validate a catalog YAML file
`)
		return 1
	}
	switch args[0] {
	case "validate":
		return runCatalogValidate(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, `usage: gh actionpins catalog <subcommand>

Subcommands:
  validate    Load and validate a catalog YAML file
`)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown catalog subcommand %q\n", args[0])
		return 1
	}
}

func runCatalogValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("catalog validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog YAML (default: user config actionpins/catalog.yaml)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path, c, code := loadCatalog(*catalogPath, stderr)
	if code != 0 {
		return code
	}

	if len(c.Repos) > 0 {
		_, _ = fmt.Fprintf(stdout, "catalog OK: %s (%d actions, %d repos)\n", path, len(c.Actions), len(c.Repos))
	} else {
		_, _ = fmt.Fprintf(stdout, "catalog OK: %s (%d actions)\n", path, len(c.Actions))
	}
	return 0
}

// loadCatalog resolves path (default when empty), loads, and validates.
// On error it writes to stderr and returns a non-zero code.
func loadCatalog(catalogPath string, stderr io.Writer) (path string, cat *catalog.Catalog, code int) {
	path = catalogPath
	if path == "" {
		var err error
		path, err = catalog.DefaultPath()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return "", nil, 1
		}
	}
	cat, err := catalog.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return "", nil, 1
	}
	return path, cat, 0
}

// loadManagedRepos loads the catalog and returns resolved managed repos.
func loadManagedRepos(catalogPath string) ([]catalog.ResolvedRepo, error) {
	path := catalogPath
	if path == "" {
		var err error
		path, err = catalog.DefaultPath()
		if err != nil {
			return nil, fmt.Errorf("resolve catalog path: %w", err)
		}
	}
	cat, err := catalog.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}
	repos, err := cat.ResolveRepos()
	if err != nil {
		return nil, fmt.Errorf("resolve managed repos: %w", err)
	}
	return repos, nil
}

func writeRepoHeader(w io.Writer, repo catalog.ResolvedRepo, leadingBlank bool) error {
	if leadingBlank {
		if _, err := fmt.Fprintln(w); err != nil {
			return fmt.Errorf("write repo header: %w", err)
		}
	}
	// Include both name and path when name is set so operators can map output
	// to checkouts; path-only entries print a single identity.
	var err error
	if repo.Name != "" {
		_, err = fmt.Fprintf(w, "=== %s (%s) ===\n", repo.Name, repo.Path)
	} else {
		_, err = fmt.Fprintf(w, "=== %s ===\n", repo.Path)
	}
	if err != nil {
		return fmt.Errorf("write repo header: %w", err)
	}
	return nil
}

func isJSONFormat(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), scan.FormatJSON)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}
