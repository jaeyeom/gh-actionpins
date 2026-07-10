# gh-actionpins

Multi-repo GitHub Actions pin catalog: trusted versions with SHAs, selective apply, controlled bumps.

`gh-actionpins` is a [GitHub CLI](https://cli.github.com/) extension. A central catalog of approved action versions (commit SHAs) is the source of truth. You scan and diff real workflow usage, apply pins only to actions each repo already uses, and bump the catalog through an explicit soak/approve path—not day-0 auto-trust of `latest`.

> **Status:** foundation in progress. Catalog load/validate, local `scan`, and `diff` are available; apply lands in a follow-up issue ([#1](https://github.com/jaeyeom/gh-actionpins/issues/1)).

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

## Scan

Inventory GitHub Actions references from a local checkout by parsing workflow `uses:` lines.

```bash
# Scan the current repository (walks .github/workflows/**)
gh actionpins scan

# Scan another path
gh actionpins scan /path/to/repo

# Machine-readable output (flags before optional path)
gh actionpins scan --format json
gh actionpins scan --format json /path/to/repo
```

| Format | Description |
|--------|-------------|
| `table` (default) | Columns: `FILE`, `LINE`, `ACTION`, `REF` — stable order for humans and scripts |
| `json` | `{ "root", "findings": [ { file, line, action, ref, uses } ] }` |

**Included:** `owner/name@ref` and `owner/name/path@ref` (including reusable workflows).

**Skipped (v1):** local actions (`./...`, `../...`) and Docker images (`docker://...`). Only `.github/workflows/**` `*.yml` / `*.yaml` files are walked (composite actions under `.github/actions` are out of scope for scan v1).

Output is deterministic for the same inputs (sorted by file, line, action, ref).

## Diff

Compare discovered workflow refs against the trusted catalog and report drift.

```bash
# Diff the current repository against the default catalog
gh actionpins diff

# Explicit catalog + path
gh actionpins diff --catalog examples/catalog.yaml
gh actionpins diff --catalog examples/catalog.yaml /path/to/repo

# Machine-readable output
gh actionpins diff --format json --catalog examples/catalog.yaml
```

| Status | Meaning |
|--------|---------|
| `ok` | Ref matches the catalog SHA (and `# version` comment when `policy.require_comment` is true) |
| `mismatch` | Full SHA differs from the catalog, or SHA matches but version comment policy fails |
| `unpinned` | Catalogued action still uses a floating tag/branch (not a 40-char commit SHA) |
| `unknown` | Action is not present in the catalog |

**Exit codes (CI-friendly):**

| Code | Meaning |
|------|---------|
| `0` | No drift — every finding is `ok`, or there are no findings |
| `1` | Drift present (`mismatch`, `unpinned`, or `unknown`), or catalog/scan failure |
| `2` | Invalid usage or flags |

Table output includes a final `summary: clean|drift  ok=… mismatch=… unpinned=… unknown=…` line. JSON includes `entries` and `summary` (with `drift` bool).

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
