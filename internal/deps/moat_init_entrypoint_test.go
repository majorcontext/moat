package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/initbin"
)

// TestWriteEntrypointGoBinary asserts the image layout: the compiled
// moat-init binary is COPYed from a local context file and set as the
// ENTRYPOINT, with no network fetch or build stage.
func TestWriteEntrypointGoBinary(t *testing.T) {
	result, err := GenerateDockerfile(nil, &ImageSpec{NeedsSSH: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	df := result.Dockerfile

	for _, want := range []string{
		"COPY moat-init /usr/local/bin/moat-init\n",
		"RUN chmod +x /usr/local/bin/moat-init\n",
		"ENTRYPOINT [\"/usr/local/bin/moat-init\"]\n",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile missing %q\nGenerated Dockerfile:\n%s", want, df)
		}
	}

	if got := result.ContextFiles["moat-init"]; string(got) != string(initbin.Binary()) {
		t.Error("context file moat-init does not carry the arch-matched embedded binary")
	}

	// Offline-build contract: the entrypoint is materialized from embedded
	// bytes, never fetched or compiled at image build time.
	if strings.Contains(df, "FROM golang") {
		t.Errorf("Dockerfile uses a golang build stage for the entrypoint:\n%s", df)
	}
	for _, line := range strings.Split(df, "\n") {
		if strings.Contains(line, "moat-init") && strings.Contains(line, "curl") {
			t.Errorf("entrypoint line fetches over the network: %q", line)
		}
	}
}

// TestWriteEntrypointCompanionNoInit asserts the companion case: images that
// do not need moat-init get no entrypoint binary and no ENTRYPOINT.
func TestWriteEntrypointCompanionNoInit(t *testing.T) {
	result, err := GenerateDockerfile(nil, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if _, ok := result.ContextFiles["moat-init"]; ok {
		t.Error("context file moat-init present in a no-init image")
	}
	if strings.Contains(result.Dockerfile, "ENTRYPOINT") {
		t.Errorf("no-init image should not set an ENTRYPOINT:\n%s", result.Dockerfile)
	}
}

// TestInitHashComponentReKeys asserts the cache-key salt bump: the moat-init
// component uses the v3 label and hashes the embedded binary, so a warm-cache
// lookup cannot resolve to an image cached under the v1 (script) or v2
// (script+dispatcher+binary) scheme.
func TestInitHashComponentReKeys(t *testing.T) {
	comp := initHashComponent()
	if !strings.HasPrefix(comp, "moat-init-v3:") {
		t.Fatalf("initHashComponent() = %q, want moat-init-v3: prefix", comp)
	}

	// The component is the binary hash under the v3 label...
	sum := sha256.Sum256(initbin.Binary())
	want := "moat-init-v3:" + hex.EncodeToString(sum[:])[:8]
	if comp != want {
		t.Errorf("initHashComponent() = %q, want %q", comp, want)
	}

	// ...and the earlier scheme labels are gone (both directions of the
	// drift guard: no v1 "moat-init:" prefix, no v2 label).
	if strings.HasPrefix(comp, "moat-init:") || strings.HasPrefix(comp, "moat-init-v2:") {
		t.Errorf("initHashComponent() = %q still uses a superseded label", comp)
	}

	// A pre-cutover v2-scheme tag must not satisfy a current lookup.
	tag := ImageTag(nil, &ImageSpec{NeedsSSH: true})
	oldV2 := func() string {
		h := sha256.Sum256([]byte(",ssh:agent,moat-init-v2:deadbeef"))
		return "moat/run:" + hex.EncodeToString(h[:])[:16]
	}()
	if tag == oldV2 {
		t.Error("ImageTag matches a v2-scheme tag; cache was not re-keyed")
	}
}
