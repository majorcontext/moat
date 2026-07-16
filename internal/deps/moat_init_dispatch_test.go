package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/initbin"
)

// TestWriteEntrypointDualShip asserts the migration-window image layout: the
// dispatcher is the ENTRYPOINT at the original moat-init path, with the shell
// script and the Go binary installed next to it, all from local context files
// (no network fetch at image build time).
func TestWriteEntrypointDualShip(t *testing.T) {
	result, err := GenerateDockerfile(nil, &ImageSpec{NeedsSSH: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	df := result.Dockerfile

	for _, want := range []string{
		"COPY moat-init-dispatch.sh /usr/local/bin/moat-init-dispatch\n",
		"COPY moat-init.sh /usr/local/bin/moat-init.sh\n",
		"COPY moat-init /usr/local/bin/moat-init\n",
		"RUN chmod +x /usr/local/bin/moat-init-dispatch /usr/local/bin/moat-init.sh /usr/local/bin/moat-init\n",
		"ENTRYPOINT [\"/usr/local/bin/moat-init-dispatch\"]\n",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile missing %q\nGenerated Dockerfile:\n%s", want, df)
		}
	}

	if got := string(result.ContextFiles["moat-init.sh"]); got != MoatInitScript {
		t.Error("context file moat-init.sh does not carry MoatInitScript")
	}
	if got := string(result.ContextFiles["moat-init-dispatch.sh"]); got != MoatInitDispatcher {
		t.Error("context file moat-init-dispatch.sh does not carry MoatInitDispatcher")
	}
	if got := result.ContextFiles["moat-init"]; string(got) != string(initbin.Binary()) {
		t.Error("context file moat-init does not carry the arch-matched embedded binary")
	}

	// Offline-build contract: the entrypoint must be materialized from
	// embedded bytes, never fetched or compiled at image build time.
	if strings.Contains(df, "curl") && strings.Contains(df, "moat-init") {
		for _, line := range strings.Split(df, "\n") {
			if strings.Contains(line, "moat-init") && strings.Contains(line, "curl") {
				t.Errorf("entrypoint line fetches over the network: %q", line)
			}
		}
	}
	if strings.Contains(df, "FROM golang") {
		t.Errorf("Dockerfile uses a golang build stage for the entrypoint:\n%s", df)
	}
}

// TestWriteEntrypointCompanionNoInit asserts the companion case: images that
// do not need moat-init get none of the entrypoint pieces.
func TestWriteEntrypointCompanionNoInit(t *testing.T) {
	result, err := GenerateDockerfile(nil, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	for _, name := range []string{"moat-init.sh", "moat-init-dispatch.sh", "moat-init"} {
		if _, ok := result.ContextFiles[name]; ok {
			t.Errorf("context file %s present in a no-init image", name)
		}
	}
	if strings.Contains(result.Dockerfile, "ENTRYPOINT") {
		t.Errorf("no-init image should not set an ENTRYPOINT:\n%s", result.Dockerfile)
	}
}

// TestDispatcherContract pins the dispatcher's load-bearing properties: the
// closed MOAT_INIT_IMPL/MOAT_INIT_LEGACY enum (fatal on anything else),
// unsetting both before the handoff, and exec (never fork+wait) into the
// selected implementation.
func TestDispatcherContract(t *testing.T) {
	d := MoatInitDispatcher
	for _, want := range []string{
		`impl="${MOAT_INIT_IMPL:-sh}"`,
		"unset MOAT_INIT_IMPL MOAT_INIT_LEGACY",
		"exec /usr/local/bin/moat-init \"$@\"",
		"exec /usr/local/bin/moat-init.sh \"$@\"",
		"Error: invalid MOAT_INIT_IMPL",
		"Error: invalid MOAT_INIT_LEGACY",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("dispatcher missing %q", want)
		}
	}
	// The enum is read once, before any phase: the dispatcher must not
	// invoke either implementation by any means other than exec.
	if strings.Count(d, "exec ") != 2 {
		t.Errorf("dispatcher should exec exactly twice (go leg, sh leg); got %d", strings.Count(d, "exec "))
	}
}

// TestInitHashComponentReKeys asserts the cache-key salt bump: the moat-init
// component no longer matches the pre-dispatcher scheme (label or value), so
// images cached before the dual-ship cannot satisfy a post-dual-ship lookup.
func TestInitHashComponentReKeys(t *testing.T) {
	comp := initHashComponent()

	if !strings.HasPrefix(comp, "moat-init-v2:") {
		t.Fatalf("initHashComponent() = %q, want moat-init-v2: prefix", comp)
	}

	// The pre-commit component was "moat-init:" + sha256(script)[:8]. Assert
	// both directions: the old label is gone, and the new value is not the
	// old value under a new name (it must fold in the dispatcher + binary).
	oldHash := sha256.Sum256([]byte(MoatInitScript))
	oldValue := hex.EncodeToString(oldHash[:])[:8]
	if strings.HasPrefix(comp, "moat-init:") {
		t.Errorf("initHashComponent() = %q still uses the v1 label", comp)
	}
	if strings.HasSuffix(comp, oldValue) {
		t.Errorf("initHashComponent() = %q hashes only the script; must include dispatcher + binary", comp)
	}

	// And the tag itself changes for an init-bearing spec vs the v1 scheme.
	tag := ImageTag(nil, &ImageSpec{NeedsSSH: true})
	oldInput := ",ssh:agent,moat-init:" + oldValue
	oldTag := func() string {
		h := sha256.Sum256([]byte(oldInput))
		return "moat/run:" + hex.EncodeToString(h[:])[:16]
	}()
	if tag == oldTag {
		t.Error("ImageTag matches the pre-dispatcher tag; cache was not re-keyed")
	}
}
