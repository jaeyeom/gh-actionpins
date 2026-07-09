# gh-actionpins Makefile
# Precompiled Go GitHub CLI extension for trusted Actions pin management.

BINARY  := gh-actionpins
MODULE  := github.com/jaeyeom/gh-actionpins
GOFLAGS ?=
PKG     := ./...
CMD     := ./cmd/gh-actionpins

GOLANGCI_CONFIG_HASH_FILE := .tmp/golangci.yml.hash

# ── Aggregate targets ───────────────────────────────────────────────

.PHONY: all check help

## all: full local workflow (format, lint-fix, test, build)
all: format fix test build

## check: CI-safe checks (no mutation)
check: check-format lint test build

## help: list common targets
help:
	@echo "Common targets:"
	@echo "  all            format + fix + test + build"
	@echo "  check          check-format + lint + test + build (CI-safe)"
	@echo "  build          compile ./$(BINARY)"
	@echo "  test           go test $(PKG)"
	@echo "  lint           vet + golangci-lint"
	@echo "  format         gofmt -w"
	@echo "  fix            format + vet + golangci --fix"
	@echo "  release-check  cross-compile release platforms"
	@echo "  coverage       write coverage.out"

# ── Build ───────────────────────────────────────────────────────────

.PHONY: build clean install release-check

## build: compile the CLI binary (local development only)
build:
	go build $(GOFLAGS) -o $(BINARY) $(CMD)

## install: install the binary into GOPATH/bin
install:
	go install $(GOFLAGS) $(CMD)

## release-check: cross-compile for all release platforms to verify release readiness
release-check:
	@echo "Verifying cross-compilation for all release platforms..."
	@for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64; do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		output="dist/$(BINARY)-$${os}-$${arch}"; \
		if [ "$$os" = "windows" ]; then output="$${output}.exe"; fi; \
		echo "  Building $${os}/$${arch}..."; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -o "$$output" $(CMD) || exit 1; \
	done
	@echo "All platforms built successfully in dist/"

## clean: remove build artifacts and coverage files
clean:
	rm -rf $(BINARY) dist coverage.out coverage.html .tmp

# ── Format ──────────────────────────────────────────────────────────

.PHONY: format check-format

## format: auto-format all Go source files
format:
	gofmt -w .

## check-format: verify formatting (fails on diff)
check-format:
	@test -z "$$(gofmt -l .)" || { echo "gofmt: files need formatting:"; gofmt -l .; exit 1; }

# ── Lint / Fix ──────────────────────────────────────────────────────

.PHONY: lint fix vet lint-golangci fix-golangci verify-golangci-config

## lint: run go vet and golangci-lint
lint: vet lint-golangci

# fix depends on format so both don't mutate files concurrently under make -j.
## fix: format, vet, and golangci-lint auto-fix
fix: format
fix: vet fix-golangci

## vet: run go vet on all packages
vet:
	go vet $(PKG)

## lint-golangci: run golangci-lint
lint-golangci: verify-golangci-config
	golangci-lint run $(PKG)

## fix-golangci: run golangci-lint with auto-fix
fix-golangci: verify-golangci-config
	golangci-lint run --fix $(PKG)

## verify-golangci-config: validate .golangci.yml (cached by content hash)
verify-golangci-config:
	@CURRENT_HASH=$$(shasum -a 256 .golangci.yml | cut -d' ' -f1); \
	mkdir -p .tmp; \
	if [ ! -f $(GOLANGCI_CONFIG_HASH_FILE) ] || [ "$$(cat $(GOLANGCI_CONFIG_HASH_FILE))" != "$$CURRENT_HASH" ]; then \
		echo "Verifying golangci-lint config..."; \
		golangci-lint config verify && echo "$$CURRENT_HASH" > $(GOLANGCI_CONFIG_HASH_FILE); \
	fi

# ── Test ────────────────────────────────────────────────────────────

.PHONY: test coverage coverage-html coverage-report clean-coverage

## test: run all unit tests
test:
	go test $(PKG)

## coverage: generate coverage profile
coverage:
	go test -coverprofile=coverage.out $(PKG)

## coverage-html: write HTML coverage report
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html

## coverage-report: print coverage summary to stdout
coverage-report: coverage
	go tool cover -func=coverage.out

## clean-coverage: remove coverage artifacts
clean-coverage:
	rm -f coverage.out coverage.html

# ── Module maintenance ──────────────────────────────────────────────

.PHONY: tidy verify

## tidy: tidy go.mod / go.sum
tidy:
	go mod tidy

## verify: verify module checksums
verify:
	go mod verify
