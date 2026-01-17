//go:build ignore

// This file is used by go generate to build the embedded credential helper binaries.
// Run: go generate ./internal/run/...
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	// Find the project root (two levels up from internal/run)
	dir, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	projectRoot := filepath.Join(dir, "..", "..")

	// Build for linux/amd64
	fmt.Println("Building aws-credential-helper for linux/amd64...")
	if err := build(projectRoot, "amd64"); err != nil {
		fatal(err)
	}

	// Build for linux/arm64
	fmt.Println("Building aws-credential-helper for linux/arm64...")
	if err := build(projectRoot, "arm64"); err != nil {
		fatal(err)
	}

	fmt.Println("Done.")
}

func build(projectRoot, arch string) error {
	output := filepath.Join(projectRoot, "internal/run/helpers/aws-credential-helper-linux-"+arch)
	cmd := exec.Command("go", "build", "-ldflags=-s -w", "-o", output, "./cmd/aws-credential-helper")
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		"GOARCH="+arch,
		"CGO_ENABLED=0",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "generate: %v\n", err)
	os.Exit(1)
}
