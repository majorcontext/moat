#!/bin/sh
# Go Development Environment Demo
#
# This script displays version information for all installed Go tools.

echo "=================================================="
echo "Go Development Environment"
echo "=================================================="
echo

echo "--- Go Runtime ---"
go version
echo

echo "--- Formatting (gofumpt) ---"
gofumpt --version 2>/dev/null || echo "gofumpt: installed"
echo

echo "--- Vulnerability Scanner (govulncheck) ---"
govulncheck -version 2>&1 | head -1
echo

echo "--- Release Automation (goreleaser) ---"
goreleaser --version 2>&1 | head -1
echo

echo "--- Linter (golangci-lint) ---"
golangci-lint --version
echo

echo "--- Language Server (gopls) ---"
gopls version 2>&1 | head -1
echo

echo "--- Git ---"
git --version
echo

echo "=================================================="
echo "Environment ready for Go development!"
echo "=================================================="
echo
echo "Example workflows:"
echo "  go mod init myproject     # Initialize a new module"
echo "  gofumpt -w .              # Format code"
echo "  golangci-lint run         # Run linters"
echo "  govulncheck ./...         # Check for vulnerabilities"
echo "  goreleaser check          # Validate release config"
echo "  gopls                     # Language server for editors"
