package deps

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/sandbox"
)

// The kernel sandbox must force BOTH a custom image and the moat-init
// entrypoint: the moat-sandbox helper is COPY'd into the image, and
// moat-init.sh's final exec chain is what routes through it. Without either,
// MOAT_SANDBOX_POLICY would be set but never honored.
func TestNeedsKernelSandboxForcesCustomImageAndInit(t *testing.T) {
	if !(&ImageSpec{NeedsKernelSandbox: true}).NeedsCustomImage(false) {
		t.Error("NeedsKernelSandbox should force NeedsCustomImage true")
	}
	if !(&ImageSpec{NeedsKernelSandbox: true}).needsInit("") {
		t.Error("NeedsKernelSandbox should force needsInit true")
	}
	// Companion cases live in TestNeedsWorkspaceVolumeForcesCustomImageAndInit:
	// an empty spec needs neither a custom image nor the entrypoint.
}

func TestGenerateDockerfileKernelSandbox(t *testing.T) {
	result, err := GenerateDockerfile(nil, &ImageSpec{NeedsKernelSandbox: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.Contains(result.Dockerfile, "COPY moat-sandbox "+sandbox.HelperPath) {
		t.Errorf("Dockerfile should COPY the moat-sandbox helper, got:\n%s", result.Dockerfile)
	}
	if _, ok := result.ContextFiles["moat-sandbox"]; !ok {
		t.Error("ContextFiles should carry the moat-sandbox helper binary")
	}
	// The helper is useless without the entrypoint that routes exec through it.
	if !strings.Contains(result.Dockerfile, "ENTRYPOINT [\"/usr/local/bin/moat-init\"]") {
		t.Error("Dockerfile should keep the moat-init ENTRYPOINT")
	}
}

func TestGenerateDockerfileWithoutKernelSandbox(t *testing.T) {
	// Companion: a spec that needs init for another reason must not ship the
	// helper binary or its COPY line.
	result, err := GenerateDockerfile(nil, &ImageSpec{NeedsGitIdentity: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if strings.Contains(result.Dockerfile, "moat-sandbox") {
		t.Errorf("Dockerfile should not reference moat-sandbox, got:\n%s", result.Dockerfile)
	}
	if _, ok := result.ContextFiles["moat-sandbox"]; ok {
		t.Error("ContextFiles should not carry moat-sandbox when the kernel sandbox is off")
	}
}

func TestImageTagKernelSandbox(t *testing.T) {
	deps := []Dependency{{Name: "node", Version: "22"}}
	with := ImageTag(deps, &ImageSpec{NeedsKernelSandbox: true})
	without := ImageTag(deps, &ImageSpec{})
	if with == without {
		t.Error("ImageTag should differ when the kernel sandbox is toggled (image carries the helper binary)")
	}
	// Determinism companion: same spec, same tag.
	if with != ImageTag(deps, &ImageSpec{NeedsKernelSandbox: true}) {
		t.Error("ImageTag should be deterministic for the same spec")
	}
}
