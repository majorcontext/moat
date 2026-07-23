.PHONY: all help build build-cli generate-sandbox restore-sandbox-stubs test test-unit test-e2e test-bats lint fix clean coverage snapshot

# Committed fail-closed placeholders for the embedded moat-sandbox helper.
# `go generate ./internal/sandboxbin` overwrites them with real cross-compiled
# binaries at build time; they must never be committed in that state.
SANDBOX_STUBS := internal/sandboxbin/embed/moat-sandbox-linux-amd64 internal/sandboxbin/embed/moat-sandbox-linux-arm64 internal/sandboxbin/checksums.txt

# Default target - running "make" shows help
all: help

help: ## Show this help message
	@echo "Available targets:"
	@echo ""
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Examples:"
	@echo "  make test                    # Run all tests"
	@echo "  make test-unit               # Run only unit tests"
	@echo "  make test-e2e                # Run only E2E tests"
	@echo "  make test-unit ARGS='-run TestName'           # Run specific unit test"
	@echo "  make test-unit ARGS='-run TestName ./internal/proxy'"  # Run test in specific package"

build: ## Build the project
	go build ./...

build-cli: ## Build the CLI binary ./moat (regenerates the embedded moat-sandbox binaries, then restores the committed stubs)
	@go generate ./internal/sandboxbin && \
	go build -ldflags "-s -w -X github.com/majorcontext/moat/cmd/moat/cli.version=dev -X github.com/majorcontext/moat/cmd/moat/cli.commit=$$(git rev-parse --short HEAD) -X github.com/majorcontext/moat/cmd/moat/cli.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o moat ./cmd/moat; rc=$$?; \
	git checkout -- $(SANDBOX_STUBS); exit $$rc

generate-sandbox: ## Cross-compile cmd/moat-sandbox into internal/sandboxbin/embed (over the committed stubs; run 'make restore-sandbox-stubs' before committing)
	go generate ./internal/sandboxbin

restore-sandbox-stubs: ## Restore the committed moat-sandbox stub blobs after a manual generate-sandbox
	git checkout -- $(SANDBOX_STUBS)

test: test-unit test-e2e test-bats ## Run all tests (unit + E2E + hooks)

test-unit: ## Run unit tests with race detector (use ARGS for filtering, e.g., ARGS='-run TestName')
	go test -race $(ARGS) ./...

test-e2e: ## Run E2E tests (regenerates the embedded moat-sandbox binaries, then restores the committed stubs; use ARGS for filtering)
	@go generate ./internal/sandboxbin && \
	go test -tags=e2e -timeout=30m $(ARGS) ./internal/e2e/; rc=$$?; \
	git checkout -- $(SANDBOX_STUBS); exit $$rc

test-bats: ## Run bats tests for Claude Code hooks
	@which bats > /dev/null || (echo "bats not installed. Install from https://github.com/bats-core/bats-core" && exit 1)
	bats .claude/hooks/

lint: ## Run linter (requires golangci-lint v2)
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Install from https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run

fix: ## Auto-fix linter and formatter issues (requires golangci-lint v2)
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Install from https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run --fix

coverage: ## Generate test coverage report
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

snapshot: ## Build a local release snapshot with GoReleaser
	@which goreleaser > /dev/null || (echo "goreleaser not installed. Install from https://goreleaser.com/install/" && exit 1)
	goreleaser release --snapshot --clean

clean: ## Clean build artifacts and coverage files
	rm -f coverage.out coverage.out coverage.html
	rm -rf dist/
	go clean
