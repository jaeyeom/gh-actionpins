// Command gh-actionpins is a GitHub CLI extension that manages trusted
// GitHub Actions pins across a personal or org repo fleet.
//
// Install: gh extension install jaeyeom/gh-actionpins
// Run:     gh actionpins --help
package main

import (
	"fmt"
	"os"
)

const usage = `gh-actionpins manages trusted GitHub Actions pins.

A catalog of approved action versions (commit SHAs) is the source of truth.
Scan, diff, and apply pin those versions across managed repositories; bumps
require an explicit approve path with soak time.

Usage:
  gh actionpins <command> [flags]

Commands:
  help    Show this help

Flags:
  -h, --help    Show this help

Future commands (see repo issues):
  scan, diff, apply, check-updates, propose-bump, approve-bump

Examples:
  gh actionpins --help
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(os.Stdout, usage)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(os.Stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
		return 1
	}
}
