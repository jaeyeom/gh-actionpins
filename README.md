# gh-actionpins

Multi-repo GitHub Actions pin catalog: trusted versions with SHAs, selective apply, controlled bumps.

`gh-actionpins` is a [GitHub CLI](https://cli.github.com/) extension. A central catalog of approved action versions (commit SHAs) is the source of truth. You scan and diff real workflow usage, apply pins only to actions each repo already uses, and bump the catalog through an explicit soak/approve path—not day-0 auto-trust of `latest`.

> **Status:** foundation in progress. Catalog load/validate is available; scan/diff/apply land in follow-up issues ([#1](https://github.com/jaeyeom/gh-actionpins/issues/1)).

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

## Catalog

The trusted pin catalog is YAML. Default path: `~/.config/actionpins/catalog.yaml` (OS user config directory).

```bash
# Validate the default catalog path
gh actionpins catalog validate

# Validate a specific file (example shipped in-repo)
gh actionpins catalog validate --catalog examples/catalog.yaml
```

Example shape (see [`examples/catalog.yaml`](examples/catalog.yaml)):

```yaml
actions:
  actions/checkout:
    version: v7.0.0
    sha: 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
    approved_at: 2026-06-01
policy:
  min_age: 7d
  prefer: major          # major | same-major | patch-only
  require_comment: true
```

Invalid catalogs fail with clear errors (missing `version`/`sha`, non-40-char hex SHA, bad `min_age` duration, etc.).

## Development

```bash
make check          # check-format + lint + test + build (CI-safe)
make all            # format + fix + test + build
make build          # produce ./gh-actionpins
make test
make lint
make release-check  # cross-compile release platforms
make help           # list common targets
```

CI runs `make check` (same gate as local). Workflow actions are **SHA-pinned** with version comments (dogfooding the pin style this tool will manage).

## Roadmap

See [issue #1](https://github.com/jaeyeom/gh-actionpins/issues/1) for the MVP plan and child issues (`scan`, `diff`, `apply`, catalog, controlled bumps).
