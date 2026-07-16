.PHONY: all help build build-cli generate-init restore-init-stubs test test-unit test-e2e test-bats lint fix clean coverage snapshot

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

build: generate-init ## Build the project (regenerates the embedded moat-init binaries)
	go build ./...

build-cli: generate-init ## Build the CLI binary ./moat
	go build -ldflags "-s -w -X github.com/majorcontext/moat/cmd/moat/cli.version=dev -X github.com/majorcontext/moat/cmd/moat/cli.commit=$$(git rev-parse --short HEAD) -X github.com/majorcontext/moat/cmd/moat/cli.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o moat ./cmd/moat

generate-init: ## Cross-compile cmd/moat-init into internal/initbin/embed (over the committed stubs)
	go generate ./internal/initbin

restore-init-stubs: ## Restore the committed moat-init stub blobs after a local build
	git checkout -- internal/initbin/embed internal/initbin/checksums.txt

test: test-unit test-e2e test-bats ## Run all tests (unit + E2E + hooks)

test-unit: ## Run unit tests with race detector (use ARGS for filtering, e.g., ARGS='-run TestName')
	go test -race $(ARGS) ./...

test-e2e: generate-init ## Run E2E tests (use ARGS for filtering, e.g., ARGS='-run TestName')
	go test -tags=e2e -timeout=30m $(ARGS) ./internal/e2e/

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
