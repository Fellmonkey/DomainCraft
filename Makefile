DOMAIN ?= domain.yaml
BRIDGE ?= bridges/csharp
OUTPUT ?= generated
BRIDGE_NAME ?= csharp
SPEC_OUTPUT ?= spec/domain.schema.json
GUI_DIR ?= ../domaincraft-studio
SKILLS_DIR ?= ../domaincraft-skills/domaincraft-core
GO_CACHE_DIR ?= $(CURDIR)/.gocache
GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION ?= v1.9.0

# Release version stamped into the CLI (and WASM validator) binary.
ifeq ($(OS),Windows_NT)
VERSION ?= $(shell git describe --tags --exact-match 2>nul || echo dev)
else
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
endif

# Cross-platform file copy used by the spec-distribution targets.
ifeq ($(OS),Windows_NT)
COPY_CMD = copy /Y "$(subst /,\,$(SPEC_OUTPUT))" "$(subst /,\,$(SKILLS_DIR))\domain.schema.json"
GUI_CMD = cd /d "$(subst /,\,$(GUI_DIR))" && call npm run generate:types
else
COPY_CMD = cp "$(SPEC_OUTPUT)" "$(SKILLS_DIR)/domain.schema.json"
GUI_CMD = cd $(GUI_DIR) && npm run generate:types
endif

export GOCACHE := $(GO_CACHE_DIR)
# GOTMPDIR is deliberately not set: pointing it inside the repo makes
# t.TempDir() land under a monorepo root and breaks path-sensitive tests.

.PHONY: help build install run test test-affected contract-test contract-golden-update test-verbose test-coverage lint vulncheck vet fmt clean install-deps check-tidy dev-watch dev-test-watch cli-validate cli-generate cli-bridges regenerate-spec generate-gui-types copy-spec-skills build-wasm build-wasm-gui

help:
	@echo "DomainCraft CLI - Available Commands"
	@echo ""
	@echo "  make build           - Build the binary to bin/domaincraft"
	@echo "  make install         - Build and install to /usr/local/bin (or ~/.local/bin)"
	@echo "  make run             - Build and run with example domain.yaml"
	@echo "  make cli-validate    - Run 'validate' command (uses DOMAIN=$(DOMAIN))"
	@echo "  make cli-generate    - Run 'generate' command (uses DOMAIN=$(DOMAIN) BRIDGE=$(BRIDGE) OUTPUT=$(OUTPUT))"
	@echo "  make cli-bridges     - List available bridges"
	@echo "  make regenerate-spec - Regenerate spec/domain.schema.json, copy to skills, and regenerate GUI types"
	@echo "  make generate-gui-types - Regenerate only GUI TypeScript types from schema"
	@echo "  make copy-spec-skills - Copy regenerated schema to domaincraft-skills/domaincraft-core"
	@echo "  make build-wasm      - Build WASM validator binary (GOOS=js GOARCH=wasm)"
	@echo "  make build-wasm-gui  - Build WASM and copy to domaincraft-studio/public/wasm/"
	@echo "  make test            - Run all tests (test cache disabled)"
	@echo "  make test-affected   - Run tests for packages touched by uncommitted changes"
	@echo "  make contract-test   - Run contract tests (golden output + determinism)"
	@echo "  make contract-golden-update - Regenerate golden manifests after an intentional output change"
	@echo "  make test-verbose    - Run tests with verbose output"
	@echo "  make test-coverage   - Run tests and generate coverage report"
	@echo "  make lint            - Run golangci-lint via go run (same gate as CI)"
	@echo "  make vulncheck       - Run govulncheck via go run"
	@echo "  make vet             - Run go vet ./..."
	@echo "  make fmt             - Format code (gofmt)"
	@echo "  make clean           - Clean build artifacts"
	@echo "  make install-deps    - Download dependencies (go mod download)"
	@echo "  make check-tidy      - Fail if go mod tidy would change go.mod/go.sum"
	@echo "  make dev-watch       - Rebuild/run on file changes (requires entr)"
	@echo "  make dev-test-watch  - Run tests on file changes (requires entr)"

install-deps:
	@echo "Downloading dependencies..."
	@go mod download

# Check only: go mod tidy must not mutate go.mod/go.sum in CI.
check-tidy:
	@echo "Checking go.mod/go.sum are tidy..."
	@go mod tidy
	@git diff --exit-code -- go.mod go.sum

build: install-deps
	@echo "Building domaincraft ($(VERSION))..."
	@go build -ldflags "-X main.version=$(VERSION)" -o bin/domaincraft ./cmd/domaincraft

install: build
	@echo "Installing domaincraft..."
ifeq ($(OS),Windows_NT)
	@cmd /c "if not exist %LOCALAPPDATA%\DomainCraft mkdir %LOCALAPPDATA%\DomainCraft"
	@cmd /c "copy /Y bin\domaincraft %LOCALAPPDATA%\DomainCraft\domaincraft.exe >nul"
	@echo "Installed to %LOCALAPPDATA%\DomainCraft\domaincraft.exe"
	@echo "Make sure %LOCALAPPDATA%\DomainCraft is in your PATH"
else
	@if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then \
		cp bin/domaincraft /usr/local/bin/domaincraft; \
		echo "Installed to /usr/local/bin/domaincraft"; \
	elif [ -d "$$HOME/.local/bin" ]; then \
		mkdir -p "$$HOME/.local/bin"; \
		cp bin/domaincraft "$$HOME/.local/bin/domaincraft"; \
		echo "Installed to $$HOME/.local/bin/domaincraft"; \
	else \
		mkdir -p "$$HOME/.local/bin"; \
		cp bin/domaincraft "$$HOME/.local/bin/domaincraft"; \
		echo "Installed to $$HOME/.local/bin/domaincraft"; \
		echo "Make sure ~/.local/bin is in your PATH"; \
	fi
endif

run: build
	@echo "Running domaincraft with example domain.yaml..."
	@./bin/domaincraft

# Convenience targets for development (via go run)
cli-validate:
	@go run ./cmd/domaincraft validate --domain $(DOMAIN)

cli-generate:
	@go run ./cmd/domaincraft generate --domain $(DOMAIN) --bridge $(BRIDGE) --output $(OUTPUT)

cli-bridges:
	@go run ./cmd/domaincraft bridges

regenerate-spec:
	@echo "Running: go run ./cmd/schema-gen -o $(SPEC_OUTPUT)"
	@go run ./cmd/schema-gen -o $(SPEC_OUTPUT)
	@$(MAKE) --no-print-directory copy-spec-skills
	@$(MAKE) --no-print-directory generate-gui-types

generate-gui-types:
ifeq ($(wildcard $(GUI_DIR)/package.json),)
	@echo "WARNING: GUI directory not found at $(GUI_DIR), skipping GUI types."
else
	@echo "Generating TypeScript types for GUI..."
	@$(GUI_CMD)
endif

copy-spec-skills:
ifeq ($(wildcard $(SKILLS_DIR)),)
	@echo "WARNING: skills directory not found at $(SKILLS_DIR), skipping schema copy."
else
	@echo "Copying schema to skills..."
	@$(COPY_CMD)
	@echo "Copied schema to $(SKILLS_DIR)/domain.schema.json"
endif

# -count=1: hashing the embedded bridges tree (3454 files) for the test cache
# costs ~25s on Windows, more than the tests themselves (~7s).
test:
	@echo "Running tests..."
	@go test -count=1 ./...

# Run only packages affected by uncommitted changes and their dependents.
test-affected:
ifeq ($(OS),Windows_NT)
	@echo "test-affected requires Git Bash with bash and python3 on PATH. Install Git Bash, then run 'make test-affected' from a Git Bash shell."
else
	@bash scripts/test-impact.sh
endif

# Contract tests (Phase 2): golden output + determinism for bundled bridges.
contract-test:
	@go test ./internal/contract/

# Regenerate the golden manifests after an intentional output change.
contract-golden-update:
	@go test ./internal/contract/ -run TestGoldenLeafBridges -update

test-verbose:
	@echo "Running tests (verbose)..."
	@go test -v -count=1 ./...

test-coverage:
	@echo "Running tests with coverage..."
	@go test -cover ./...
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

lint:
	@echo "Running golangci-lint $(GOLANGCI_LINT_VERSION)..."
	@go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

vulncheck:
	@echo "Running govulncheck $(GOVULNCHECK_VERSION)..."
	@go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

vet:
	@echo "Running go vet..."
	@go vet ./...

fmt:
	@echo "Formatting code..."
	@go fmt ./...

clean:
ifeq ($(OS),Windows_NT)
	@echo "Cleaning build artifacts..."
	@cmd /c "if exist bin rmdir /S /Q bin"
	@cmd /c "if exist coverage.out del /Q coverage.out"
	@cmd /c "if exist coverage.html del /Q coverage.html"
else
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@rm -f coverage.out coverage.html
endif

# Development targets
dev-watch:
ifeq ($(OS),Windows_NT)
	@echo "dev-watch requires Git Bash + entr. Install both, then run 'make dev-watch' from a Git Bash shell."
else
	@echo "Watching for changes (requires entr)..."
	@find . -name "*.go" | entr -r make run
endif

dev-test-watch:
ifeq ($(OS),Windows_NT)
	@echo "dev-test-watch requires Git Bash + entr. Install both, then run 'make dev-test-watch' from a Git Bash shell."
else
	@echo "Watching for changes and running tests..."
	@find . -name "*.go" | entr -r make test-verbose
endif

# WASM targets
WASM_OUTPUT ?= bin/validate.wasm
WASM_GUI_DIR ?= ../domaincraft-studio/public/wasm
WASM_LDFLAGS := -X main.version=$(VERSION)

build-wasm:
	@echo "Building WASM validator ($(VERSION))..."
	@GOOS=js GOARCH=wasm go build -ldflags "$(WASM_LDFLAGS)" -o $(WASM_OUTPUT) ./cmd/wasm-validator/
	@echo "Built $(WASM_OUTPUT)"

build-wasm-gui: build-wasm
	@echo "Copying WASM to GUI public directory..."
	@mkdir -p $(WASM_GUI_DIR)
	@cp $(WASM_OUTPUT) $(WASM_GUI_DIR)/validate.wasm
	@echo "Copied to $(WASM_GUI_DIR)/validate.wasm"
