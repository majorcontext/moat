// Package initbin embeds the prebuilt moat-init entrypoint binaries
// (cmd/moat-init cross-compiled static for linux/amd64 and linux/arm64) so
// the moat host binary can ship them into run images without any network
// fetch at image-build time.
//
// The committed blobs under embed/ are human-readable fail-closed stubs (a
// tiny shell script that prints a FATAL message and exits 1), so a fresh
// clone always compiles. `go generate ./internal/initbin` cross-compiles the
// real binaries over the stubs and refreshes checksums.txt; it is wired into
// `make build` and the goreleaser before hook (`go generate ./...`). Never
// commit the regenerated real blobs — `make restore-init-stubs` puts the
// stubs back.
//
// The embed directory is deliberately named embed/ (not dist/): the repo's
// .gitignore has a bare `dist/` that matches at any depth and would silently
// untrack the committed stubs.
package initbin

import (
	"bytes"
	_ "embed"
	"runtime"
)

//go:generate go run ./gen

//go:embed embed/moat-init-linux-amd64
var binAMD64 []byte

//go:embed embed/moat-init-linux-arm64
var binARM64 []byte

//go:embed checksums.txt
var Checksums string

// stubMarker identifies the committed placeholder blobs. The stub is a shell
// script (reviewable text, runs on any Linux) whose second line carries this
// marker; a real cross-compiled binary is an ELF image and can never start
// with it.
const stubMarker = "#!/bin/sh\n# moat-init-stub"

// BinaryFor returns the embedded entrypoint for a GOARCH, or nil when no
// binary is embedded for that architecture. Run images are always built for
// the host's own architecture, so runtime.GOARCH selects the right blob.
func BinaryFor(goarch string) []byte {
	switch goarch {
	case "amd64":
		return binAMD64
	case "arm64":
		return binARM64
	default:
		return nil
	}
}

// Binary returns the embedded entrypoint matching the host architecture, or
// nil on architectures moat does not build run images for.
func Binary() []byte {
	return BinaryFor(runtime.GOARCH)
}

// IsStub reports whether b is the committed fail-closed placeholder rather
// than a real cross-compiled entrypoint. Three layers keep a stub from
// serving as PID 1: the release gate (internal/initbin/gate, wired into the
// goreleaser before hooks) refuses to release stub bytes and execs the
// regenerated binary's --plan as a positive functional check; the e2e
// acceptance harness skips when the test binary embeds a stub; and the stub
// itself fails loudly at runtime — the backstop for channels that bypass
// generation entirely (`go install`, bare `go build`). Because the Go binary
// is now the sole entrypoint (no shell fallback), a stub shipped that way
// makes the container fail closed at PID 1 with the stub's rebuild message
// rather than silently skipping the privilege drop.
func IsStub(b []byte) bool {
	return bytes.HasPrefix(b, []byte(stubMarker))
}
