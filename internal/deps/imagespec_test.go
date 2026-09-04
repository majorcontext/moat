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

// Serial devices must force BOTH a custom image and the moat-init entrypoint:
// the RFC2217 broker URL handed to the container points at the synthetic
// hostname moat-host, and on Apple containers / Docker Desktop that name only
// resolves when moat-init.sh writes it to /etc/hosts from MOAT_EXTRA_HOSTS.
// Without the entrypoint the env var is delivered but unconsumed — the run
// starts, the URL is printed, and the first tool that resolves moat-host fails
// with a DNS error. Docker-on-Linux masks this via --add-host, so the gate is
// unconditional rather than platform-switched.
func TestHasSerialDevicesForcesCustomImageAndInit(t *testing.T) {
	// NeedsCustomImage: serial devices force true even with no deps (a devices-
	// only run: no grants, no agent, permissive policy).
	if !(&ImageSpec{HasSerialDevices: true}).NeedsCustomImage(false) {
		t.Error("HasSerialDevices should force NeedsCustomImage true")
	}
	// Companion: without the flag and without deps, no custom image.
	if (&ImageSpec{}).NeedsCustomImage(false) {
		t.Error("empty spec with no deps should not need a custom image")
	}

	// needsInit: serial devices force the moat-init entrypoint even with no
	// docker mode and no other init trigger.
	if !(&ImageSpec{HasSerialDevices: true}).needsInit("") {
		t.Error("HasSerialDevices should force needsInit true")
	}
	// Companion: without the flag and no other trigger, no init entrypoint.
	if (&ImageSpec{}).needsInit("") {
		t.Error("empty spec should not need the init entrypoint")
	}
}

// A proxy-registered run must get BOTH a custom image and the moat-init
// entrypoint: its HTTP_PROXY points at the synthetic hostname moat-proxy,
// which on Apple containers / Docker Desktop only resolves when moat-init.sh
// writes it to /etc/hosts from MOAT_EXTRA_HOSTS. Grant-less runs registered
// for network.host, network.rules, MCP, keep_policy, or claude.base_url hit
// exactly this — no other gate bakes the entrypoint for them. The
// Docker-on-Linux mask (--add-host) is why the gate is unconditional.
func TestNeedsProxyForcesCustomImageAndInit(t *testing.T) {
	// NeedsCustomImage: proxy registration forces true even with no deps (a
	// grant-less network.host-only run).
	if !(&ImageSpec{NeedsProxy: true}).NeedsCustomImage(false) {
		t.Error("NeedsProxy should force NeedsCustomImage true")
	}
	// Companion: without the flag and without deps, no custom image.
	if (&ImageSpec{}).NeedsCustomImage(false) {
		t.Error("empty spec with no deps should not need a custom image")
	}

	// needsInit: proxy registration forces the moat-init entrypoint even with
	// no docker mode and no other init trigger.
	if !(&ImageSpec{NeedsProxy: true}).needsInit("") {
		t.Error("NeedsProxy should force needsInit true")
	}
	// Companion: without the flag and no other trigger, no init entrypoint.
	if (&ImageSpec{}).needsInit("") {
		t.Error("empty spec should not need the init entrypoint")
	}
}
