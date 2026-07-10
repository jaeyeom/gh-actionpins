// Command gh-actionpins is a GitHub CLI extension that manages trusted
// GitHub Actions pins across a personal or org repo fleet.
//
// Install: gh extension install jaeyeom/gh-actionpins
// Run:     gh actionpins --help
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jaeyeom/gh-actionpins/internal/catalog"
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
  help                Show this help

Flags:
  -h, --help    Show this help

Future commands (see repo issues):
  diff, apply, check-updates, propose-bump, approve-bump

Catalog:
  Default path: ~/.config/actionpins/catalog.yaml (OS user config dir)
  Example:      examples/catalog.yaml

Scan:
  Walks .github/workflows/** for owner/name@ref (and owner/name/path@ref).
  Local (./...) and Docker (docker://...) uses are skipped.
  Output: table (default) or JSON (--format).

Examples:
  gh actionpins --help
  gh actionpins catalog validate
  gh actionpins catalog validate --catalog examples/catalog.yaml
  gh actionpins scan
  gh actionpins scan --format json
  gh actionpins scan --format json /path/to/repo
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
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 1
	}
}

func runScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", scan.FormatTable, "output format: table or json")
	if err := fs.Parse(args); err != nil {
		return 2
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

	path := *catalogPath
	if path == "" {
		var err error
		path, err = catalog.DefaultPath()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}

	c, err := catalog.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "catalog OK: %s (%d actions)\n", path, len(c.Actions))
	return 0
}
