// Package sandboxbin embeds the prebuilt moat-sandbox helper binaries
// (cmd/moat-sandbox cross-compiled static for linux/amd64 and linux/arm64)
// so the moat host binary can ship them into run images without any network
// fetch at image-build time. It mirrors the internal/initbin pattern from
// the moat-init Go entrypoint work (PR #441) so the two can merge later.
//
// The committed blobs under embed/ are human-readable fail-closed stubs (a
// tiny shell script that prints a FATAL message and exits 1), so a fresh
// clone always compiles. `go generate ./internal/sandboxbin` cross-compiles
// the real binaries over the stubs and refreshes checksums.txt; it is wired
// into `make build-cli` and the goreleaser before hook (`go generate ./...`).
// Never commit the regenerated real blobs — `make restore-sandbox-stubs`
// puts the stubs back.
//
// The embed directory is deliberately named embed/ (not dist/): the repo's
// .gitignore has a bare `dist/` that matches at any depth and would silently
// untrack the committed stubs.
package sandboxbin

import (
	"bytes"
	_ "embed"
	"runtime"
)

//go:generate go run ./gen

//go:embed embed/moat-sandbox-linux-amd64
var binAMD64 []byte

//go:embed embed/moat-sandbox-linux-arm64
var binARM64 []byte

//go:embed checksums.txt
var Checksums string

// stubMarker identifies the committed placeholder blobs. The stub is a shell
// script (reviewable text, runs on any Linux) whose second line carries this
// marker; a real cross-compiled binary is an ELF image and can never start
// with it.
const stubMarker = "#!/bin/sh\n# moat-sandbox-stub"

// BinaryFor returns the embedded helper for a GOARCH, or nil when no binary
// is embedded for that architecture. Run images are always built for the
// host's own architecture, so runtime.GOARCH selects the right blob.
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

// Binary returns the embedded helper matching the host architecture, or nil
// on architectures moat does not build run images for.
func Binary() []byte {
	return BinaryFor(runtime.GOARCH)
}

// IsStub reports whether b is the committed fail-closed placeholder rather
// than a real cross-compiled helper. The run manager refuses to create a
// kernel-sandboxed run from a stub build (with rebuild instructions), and
// the stub itself fails loudly at runtime — the backstop for channels that
// bypass generation entirely (`go install`, bare `go build`). A stub must
// never exec the agent unsandboxed.
func IsStub(b []byte) bool {
	return bytes.HasPrefix(b, []byte(stubMarker))
}
