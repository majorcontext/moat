.PHONY: all help build test test-unit test-e2e lint clean coverage

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

test: test-unit test-e2e ## Run all tests (unit + E2E)

test-unit: ## Run unit tests (use ARGS for filtering, e.g., ARGS='-run TestName')
	go test $(ARGS) ./...

test-e2e: ## Run E2E tests (use ARGS for filtering, e.g., ARGS='-run TestName')
	go test -tags=e2e -v $(ARGS) ./internal/e2e/

lint: ## Run linter (requires golangci-lint)
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Install from https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run

coverage: ## Generate test coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

clean: ## Clean build artifacts and coverage files
	rm -f coverage.out coverage.out coverage.html
	go clean
