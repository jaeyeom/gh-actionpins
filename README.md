# gh-actionpins

Multi-repo GitHub Actions pin catalog: trusted versions with SHAs, selective apply, controlled bumps.

`gh-actionpins` is a [GitHub CLI](https://cli.github.com/) extension. A central catalog of approved action versions (commit SHAs) is the source of truth. You scan and diff real workflow usage, apply pins only to actions each repo already uses, and bump the catalog through an explicit soak/approve path—not day-0 auto-trust of `latest`.

> **Status:** scaffold only. Commands beyond help land in follow-up issues ([#1](https://github.com/jaeyeom/gh-actionpins/issues/1)).

## Installation

Requires [GitHub CLI](https://cli.github.com/) (`gh`).

```bash
gh extension install jaeyeom/gh-actionpins
```

Verify:

```bash
gh actionpins --help
```

### Local development install

From a clone of this repo:

```bash
# Build into the current directory
make build

# Or install into your Go bin (then link/copy as a local extension if needed)
make install

# Load a local checkout as a gh extension (development)
gh extension install .
```

## Development

```bash
make check   # format check, lint, test (CI-safe)
make all     # format, fix, test, build
make build   # produce ./gh-actionpins
make test
make lint
```

CI runs the same `make check` and `make build` targets. Workflow actions are **SHA-pinned** with version comments (dogfooding the pin style this tool will manage).

## Roadmap

See [issue #1](https://github.com/jaeyeom/gh-actionpins/issues/1) for the MVP plan and child issues (`scan`, `diff`, `apply`, catalog, controlled bumps).
