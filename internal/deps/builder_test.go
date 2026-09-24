// internal/deps/builder_test.go
package deps

import (
	"strings"
	"testing"
)

func TestImageTag(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "22"},
		{Name: "typescript"},
	}
	tag := ImageTag(deps, nil)
	if !strings.HasPrefix(tag, "moat/run:") {
		t.Errorf("tag should start with moat/run:, got %s", tag)
	}
	// Tag should be deterministic
	tag2 := ImageTag(deps, nil)
	if tag != tag2 {
		t.Errorf("tags should be equal: %s != %s", tag, tag2)
	}
}

func TestImageTagDifferent(t *testing.T) {
	tag1 := ImageTag([]Dependency{{Name: "node", Version: "22"}}, nil)
	tag2 := ImageTag([]Dependency{{Name: "node", Version: "24"}}, nil)
	if tag1 == tag2 {
		t.Error("different deps should have different tags")
	}
}

func TestImageTagOrderIndependent(t *testing.T) {
	deps1 := []Dependency{{Name: "node"}, {Name: "protoc"}}
	deps2 := []Dependency{{Name: "protoc"}, {Name: "node"}}
	tag1 := ImageTag(deps1, nil)
	tag2 := ImageTag(deps2, nil)
	if tag1 != tag2 {
		t.Errorf("order should not matter: %s != %s", tag1, tag2)
	}
}

func TestImageTagWithSSH(t *testing.T) {
	deps := []Dependency{{Name: "node"}}
	tagWithoutSSH := ImageTag(deps, nil)
	tagWithSSH := ImageTag(deps, &ImageSpec{NeedsSSH: true})
	if tagWithoutSSH == tagWithSSH {
		t.Error("SSH option should affect tag")
	}
}

// A serial run must get a different tag from a plain run: the serial image
// bakes the moat-init entrypoint (for MOAT_EXTRA_HOSTS), so reusing a cached
// entrypoint-less image would leave moat-host unresolvable on Apple/Docker
// Desktop runtimes. Like NeedsProxy, the tag mechanism is the moat-init script
// hash (which mirrors needsInit()) rather than an explicit flag suffix, so a
// config that already bakes the entrypoint through another gate gains serial
// without a rebuild.
func TestImageTagWithSerialDevices(t *testing.T) {
	deps := []Dependency{{Name: "python"}}
	// A serial run gains the entrypoint, so its tag must differ from a plain
	// run's.
	tagPlain := ImageTag(deps, nil)
	tagSerial := ImageTag(deps, &ImageSpec{HasSerialDevices: true})
	if tagPlain == tagSerial {
		t.Error("HasSerialDevices should affect tag — the run gains the moat-init entrypoint")
	}
	// Determinism: same spec, same tag.
	if tagSerial != ImageTag(deps, &ImageSpec{HasSerialDevices: true}) {
		t.Error("HasSerialDevices tag should be deterministic")
	}

	// A run already baking the entrypoint via NeedsSSH keeps its tag: adding
	// devices: must not rebuild images whose init came from another gate.
	tagSSH := ImageTag(deps, &ImageSpec{NeedsSSH: true})
	tagSSHSerial := ImageTag(deps, &ImageSpec{NeedsSSH: true, HasSerialDevices: true})
	if tagSSH != tagSSHSerial {
		t.Errorf("HasSerialDevices should not change the tag when the moat-init entrypoint is already baked (NeedsSSH): %s vs %s", tagSSH, tagSSHSerial)
	}
}

// NeedsProxy bakes the moat-init entrypoint into images that would otherwise
// lack it (grant-less runs registered for network.host, rules, MCP,
// keep_policy, or base_url), and the entrypoint's presence must move the tag —
// a cached entrypoint-less image would leave moat-proxy unresolvable on
// Apple/Docker Desktop. The tag mechanism is the moat-init script hash, which
// mirrors needsInit(), so the assertion here is on that behavior rather than
// an explicit flag suffix. A run already baking the entrypoint through another
// gate keeps its tag: grant runs must not churn.
func TestImageTagWithNeedsProxy(t *testing.T) {
	deps := []Dependency{{Name: "python"}}
	// A grant-less proxy run gains the entrypoint, so its tag must differ
	// from a plain run's.
	tagPlain := ImageTag(deps, nil)
	tagProxy := ImageTag(deps, &ImageSpec{NeedsProxy: true})
	if tagPlain == tagProxy {
		t.Error("NeedsProxy should affect tag — the run gains the moat-init entrypoint")
	}
	// Determinism: same spec, same tag.
	if tagProxy != ImageTag(deps, &ImageSpec{NeedsProxy: true}) {
		t.Error("NeedsProxy tag should be deterministic")
	}

	// A run already baking the entrypoint via NeedsSSH keeps its tag: adding
	// NeedsProxy must not rebuild images whose init came from another gate.
	tagSSH := ImageTag(deps, &ImageSpec{NeedsSSH: true})
	tagSSHProxy := ImageTag(deps, &ImageSpec{NeedsSSH: true, NeedsProxy: true})
	if tagSSH != tagSSHProxy {
		t.Errorf("NeedsProxy should not change the tag when the moat-init entrypoint is already baked (NeedsSSH): %s vs %s", tagSSH, tagSSHProxy)
	}
}

func TestImageTagWithHooks(t *testing.T) {
	noHooks := ImageTag(nil, nil)
	withHooks := ImageTag(nil, &ImageSpec{
		Hooks: &HooksConfig{
			PostBuild:     "git config --global core.autocrlf input",
			PostBuildRoot: "apt-get install -y figlet",
		},
	})
	if noHooks == withHooks {
		t.Error("hooks should change the image hash")
	}

	// Different hooks should produce different tags
	hooks1 := ImageTag(nil, &ImageSpec{
		Hooks: &HooksConfig{PostBuild: "echo a"},
	})
	hooks2 := ImageTag(nil, &ImageSpec{
		Hooks: &HooksConfig{PostBuild: "echo b"},
	})
	if hooks1 == hooks2 {
		t.Error("different hooks should produce different image tags")
	}

	// pre_run should also affect hash
	withPreRun := ImageTag(nil, &ImageSpec{
		Hooks: &HooksConfig{PreRun: "npm install"},
	})
	if noHooks == withPreRun {
		t.Error("pre_run should change the image hash")
	}
}

func TestImageTagWithFirewall(t *testing.T) {
	deps := []Dependency{{Name: "python", Version: "3.11"}}
	tagWithout := ImageTag(deps, nil)
	tagWith := ImageTag(deps, &ImageSpec{NeedsFirewall: true})
	if tagWithout == tagWith {
		t.Error("firewall option should affect tag")
	}
}

func TestImageTagWithBaseImage(t *testing.T) {
	// Base image should affect tag
	tagDefault := ImageTag(nil, nil)
	tagCustom := ImageTag(nil, &ImageSpec{BaseImage: "ghcr.io/test-org/custom-base:latest"})
	if tagDefault == tagCustom {
		t.Error("base_image should change the image hash")
	}

	// Different base images should produce different tags
	tag1 := ImageTag(nil, &ImageSpec{BaseImage: "ghcr.io/test-org/custom-base:v1"})
	tag2 := ImageTag(nil, &ImageSpec{BaseImage: "ghcr.io/test-org/custom-base:v2"})
	if tag1 == tag2 {
		t.Error("different base images should produce different tags")
	}
}

func TestImageTagDockerModes(t *testing.T) {
	// docker:host and docker:dind should produce different image tags
	// because they install different packages (CLI-only vs full daemon)
	hostDeps := []Dependency{{Name: "docker", DockerMode: DockerModeHost}}
	dindDeps := []Dependency{{Name: "docker", DockerMode: DockerModeDind}}

	hostTag := ImageTag(hostDeps, nil)
	dindTag := ImageTag(dindDeps, nil)

	if hostTag == dindTag {
		t.Errorf("docker:host and docker:dind should have different tags, both got: %s", hostTag)
	}
}

func TestImageTagIncludesPiPackagesAndSettings(t *testing.T) {
	base := ImageTag(nil, &ImageSpec{})
	bake := ImageTag(nil, &ImageSpec{PiBakeSettings: true})
	if base == bake {
		t.Error("PiBakeSettings should change the image tag")
	}
	pkgsA := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:a@1"}})
	pkgsB := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:b@1"}})
	if pkgsA == bake || pkgsA == pkgsB {
		t.Error("different PiPackages should produce different tags")
	}
	// Order-independent: same set → same tag.
	ab := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:a@1", "npm:b@1"}})
	ba := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:b@1", "npm:a@1"}})
	if ab != ba {
		t.Error("PiPackages hash should be order-independent")
	}
}
