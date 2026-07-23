//go:build !linux

// moat-sandbox only runs inside Linux containers; this stub keeps
// `go build ./...` working on other platforms.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "moat: moat-sandbox only runs on Linux")
	os.Exit(1)
}
