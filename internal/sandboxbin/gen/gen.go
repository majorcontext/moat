// Command gen cross-compiles cmd/moat-sandbox into the embed/ blobs and
// refreshes checksums.txt. It is invoked by `go generate
// ./internal/sandboxbin` from the sandboxbin package directory (go generate
// sets the working directory to the directory of the file containing the
// directive).
//
// Build flags: CGO_ENABLED=0 for a static binary that runs on any base image
// (no dynamic loader / glibc dependency), -trimpath and -ldflags "-s -w" for
// reproducible, minimal blobs. The checksums are committed alongside the
// stubs; a unit test hashes the embedded bytes against checksums.txt to
// catch a stale or hand-edited blob. Note the checksums pin the Go
// toolchain: a toolchain upgrade changes the -trimpath output and fails the
// checksum test until regenerated.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const target = "github.com/majorcontext/moat/cmd/moat-sandbox"

func main() {
	arches := []string{"amd64", "arm64"}
	sums := ""
	for _, arch := range arches {
		out := filepath.Join("embed", "moat-sandbox-linux-"+arch)
		// go build -o refuses to overwrite a non-object file (the committed
		// shell-script stub), so clear the target first.
		if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "sandboxbin gen: removing %s: %v\n", out, err)
			os.Exit(1)
		}
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, target)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "sandboxbin gen: building %s for %s: %v\n", target, arch, err)
			os.Exit(1)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sandboxbin gen: reading %s: %v\n", out, err)
			os.Exit(1)
		}
		sum := sha256.Sum256(data)
		sums += hex.EncodeToString(sum[:]) + "  " + filepath.Base(out) + "\n"
	}
	if err := os.WriteFile("checksums.txt", []byte(sums), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "sandboxbin gen: writing checksums.txt: %v\n", err)
		os.Exit(1)
	}
}
