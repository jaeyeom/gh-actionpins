# gh-actionpins

Multi-repo GitHub Actions pin catalog: trusted versions with SHAs, selective apply, controlled bumps.

`gh-actionpins` is a [GitHub CLI](https://cli.github.com/) extension. A central catalog of approved action versions (commit SHAs) is the source of truth. You scan and diff real workflow usage, apply pins only to actions each repo already uses, and bump the catalog through an explicit soak/approve path—not day-0 auto-trust of `latest`.

> **Status:** core pin loop and managed fleet `--all` are available. Controlled bumps and PR apply are follow-ups ([#1](https://github.com/jaeyeom/gh-actionpins/issues/1)).

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

# Optional: managed fleet for scan/diff/apply --all
repos:
  - path: /absolute/path/to/repo-a
  - name: owner/repo-b           # optional display identity
    path: ~/src/owner/repo-b     # ~/ expands to home
```

Invalid catalogs fail with clear errors (missing `version`/`sha`, non-40-char hex SHA, bad `min_age` duration, empty `repos[].path`, etc.).

## Typical workflow

Pin a local checkout against a trusted catalog:

```bash
# 1. Start from the example catalog (or your own)
cp examples/catalog.yaml ~/.config/actionpins/catalog.yaml
# edit versions/SHAs as needed, then:
gh actionpins catalog validate

# 2. Inventory what the repo uses
gh actionpins scan

# 3. See drift vs the catalog
gh actionpins diff
# exit 1 when mismatch/unpinned/unknown — useful in CI

# 4. Preview, then apply local rewrites
gh actionpins apply --dry-run
gh actionpins apply
# review the diff, commit, open a PR yourself

# 5. Or operate the whole managed fleet (requires catalog.repos)
gh actionpins scan  --all
gh actionpins diff  --all
gh actionpins apply --all --dry-run
gh actionpins apply --all
```

**Policy choices (document them for your fleet):**

| Choice | Behavior |
|--------|----------|
| Unknown actions | Left unchanged; reported as `unknown` by `diff` and skipped by `apply` |
| Local / Docker `uses:` | Never scanned or rewritten |
| `policy.require_comment` | When true, `diff` requires `# version` and `apply` writes `owner/action@sha # version` |

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

# All managed repos from catalog.repos
gh actionpins scan --all --catalog examples/catalog.yaml
gh actionpins scan --all --format json
```

| Format | Description |
|--------|-------------|
| `table` (default) | Columns: `FILE`, `LINE`, `ACTION`, `REF` — stable order for humans and scripts |
| `json` | `{ "root", "findings": [ { file, line, action, ref, uses } ] }` |
| `json` + `--all` | `{ "repos": [ { name?, path, root, findings } ] }` |

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

# All managed repos from catalog.repos
gh actionpins diff --all --catalog examples/catalog.yaml
gh actionpins diff --all --format json
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

## Apply

Rewrite mismatched or unpinned **catalogued** workflow `uses:` lines to the trusted pin form. Unknown actions, local paths (`./…`), and Docker images (`docker://…`) are left unchanged.

```bash
# Preview rewrites without writing (recommended first)
gh actionpins apply --dry-run
gh actionpins apply --dry-run --catalog examples/catalog.yaml

# Apply to the current repository
gh actionpins apply --catalog examples/catalog.yaml

# Another checkout + machine-readable plan
gh actionpins apply --dry-run --format json /path/to/repo

# All managed repos from catalog.repos (still discovery-based per repo)
gh actionpins apply --all --dry-run --catalog examples/catalog.yaml
gh actionpins apply --all --catalog examples/catalog.yaml
```

| Target form | When |
|-------------|------|
| `owner/action@<sha> # <version>` | `policy.require_comment` is true (default style in the example catalog) |
| `owner/action@<sha>` | `policy.require_comment` is false |

**Behavior:**

- Only actions present in the catalog with status `mismatch` or `unpinned` are rewritten
- Already-correct pins (`ok`) and `unknown` actions are skipped (reported in the summary)
- Line-oriented edits preserve surrounding YAML (indentation, step names, `with:` blocks)
- Local files only — no force-push, no PR API (see roadmap)

Table output lists `FILE`, `LINE`, `ACTION`, `OLD`, `NEW` and ends with `summary: applied|would apply N change(s); skipped unknown=… ok=…`.

## Managed fleet (`--all`)

Declare local checkouts under `repos:` in the catalog. `path` is required; `name` is optional `owner/name` for display only (no network access). Leading `~/` expands to the home directory.

```yaml
repos:
  - path: /absolute/path/to/repo-a
  - name: owner/repo-b
    path: ~/src/owner/repo-b
```

Then:

```bash
gh actionpins scan  --all
gh actionpins diff  --all
gh actionpins apply --all --dry-run
gh actionpins apply --all
```

**Rules:**

- `--all` and a positional `[path]` are mutually exclusive (exit code 2)
- A non-empty `repos:` list is required; otherwise the command errors
- Each repo is still scanned independently — only actions already present in that repo are candidates for pin rewrites (never inject unused catalog actions)
- Table output prints a `=== name (path) ===` (or `=== path ===`) header per repo
- JSON wraps results as `{ "repos": [ … ] }` (diff also sets top-level `drift`)
- Per-repo failures are reported and the process continues; exit code is non-zero if any repo failed (or had drift for `diff`)

Profiles (action subsets per repo) are deferred; discovery-based inventory already avoids forcing unused actions.

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

CI runs `make check` (same gate as local). Workflow actions are **SHA-pinned** with version comments (dogfooding the pin style this tool manages).

## Roadmap

See [issue #1](https://github.com/jaeyeom/gh-actionpins/issues/1) for the full MVP plan.

| Area | Status |
|------|--------|
| Catalog load/validate | Done ([#3](https://github.com/jaeyeom/gh-actionpins/issues/3)) |
| Local `scan` / `diff` / `apply` | Done ([#4](https://github.com/jaeyeom/gh-actionpins/issues/4)–[#6](https://github.com/jaeyeom/gh-actionpins/issues/6)) |
| Managed repos + `scan`/`diff`/`apply --all` | Done ([#7](https://github.com/jaeyeom/gh-actionpins/issues/7)) |
| Controlled bumps (`check-updates` / propose / approve) | Planned ([#8](https://github.com/jaeyeom/gh-actionpins/issues/8)–[#9](https://github.com/jaeyeom/gh-actionpins/issues/9)) |
| Apply via reviewable PR (`gh`) | Planned ([#10](https://github.com/jaeyeom/gh-actionpins/issues/10)) |
