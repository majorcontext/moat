package deps

import "testing"

// Volume mode must force BOTH a custom image and the moat-init entrypoint:
// populate_workspace_volume() runs inside moat-init.sh as root. Without these,
// a grant-less / dep-less volume run gets no init entrypoint and the named
// volume is silently left empty.
func TestNeedsWorkspaceVolumeForcesCustomImageAndInit(t *testing.T) {
	// NeedsCustomImage: volume mode forces true even with no deps.
	if !(&ImageSpec{NeedsWorkspaceVolume: true}).NeedsCustomImage(false) {
		t.Error("NeedsWorkspaceVolume should force NeedsCustomImage true")
	}
	// Companion: without the flag and without deps, no custom image.
	if (&ImageSpec{}).NeedsCustomImage(false) {
		t.Error("empty spec with no deps should not need a custom image")
	}

	// needsInit: volume mode forces the moat-init entrypoint even with no docker
	// mode and no other init trigger.
	if !(&ImageSpec{NeedsWorkspaceVolume: true}).needsInit("") {
		t.Error("NeedsWorkspaceVolume should force needsInit true")
	}
	// Companion: without the flag and no other trigger, no init entrypoint.
	if (&ImageSpec{}).needsInit("") {
		t.Error("empty spec should not need the init entrypoint")
	}
}
