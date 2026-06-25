package run

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestVolumeMount(t *testing.T) {
	// bind (default type): source is the host volume dir, not a chown target.
	bindMC, isVol := volumeMount("myagent", config.VolumeConfig{Name: "cache", Target: "/c"})
	if isVol {
		t.Error("bind entry should not be reported as a named volume")
	}
	if bindMC.Volume {
		t.Error("bind entry MountConfig.Volume should be false")
	}
	if want := config.VolumeDir("myagent", "cache"); bindMC.Source != want {
		t.Errorf("bind source = %q, want %q", bindMC.Source, want)
	}
	if bindMC.Target != "/c" {
		t.Errorf("bind target = %q, want /c", bindMC.Target)
	}

	// type: volume: source is the docker volume name, and it is a chown target.
	volMC, isVol := volumeMount("myagent", config.VolumeConfig{Name: "nm", Target: "/workspace/node_modules", Type: "volume"})
	if !isVol {
		t.Error("type:volume entry should be reported as a named volume")
	}
	if !volMC.Volume {
		t.Error("volume entry MountConfig.Volume should be true")
	}
	if want := config.DockerVolumeName("myagent", "nm"); volMC.Source != want {
		t.Errorf("volume source = %q, want %q", volMC.Source, want)
	}
	if volMC.Target != "/workspace/node_modules" {
		t.Errorf("volume target = %q", volMC.Target)
	}

	// readonly must carry through to the named-volume MountConfig.
	roMC, _ := volumeMount("myagent", config.VolumeConfig{Name: "ro", Target: "/ro", Type: "volume", ReadOnly: true})
	if !roMC.ReadOnly {
		t.Error("readonly named volume should set MountConfig.ReadOnly")
	}
}

func TestVolumeChownEnv(t *testing.T) {
	paths := []string{"/workspace/node_modules", "/workspace/.pnpm-store"}

	// root entrypoint (containerUser == "") + paths → inject for moat-init.
	if env, ok := volumeChownEnv("", paths); !ok || env != "MOAT_VOLUME_CHOWN=/workspace/node_modules /workspace/.pnpm-store" {
		t.Errorf("root path: got (%q, %v), want the joined env and true", env, ok)
	}
	// explicit root entrypoint ("0:0", forced by volume mode) is also a root
	// entrypoint → inject, same as "". Without this case the companion fix in
	// volumeChownEnv/volumeOwnershipPlan would silently regress and leave config
	// named volumes owned by root in volume mode.
	if env, ok := volumeChownEnv("0:0", paths); !ok || env != "MOAT_VOLUME_CHOWN=/workspace/node_modules /workspace/.pnpm-store" {
		t.Errorf("0:0 root path: got (%q, %v), want the joined env and true", env, ok)
	}
	// non-root container (containerUser set) → omit; the helper chowns instead.
	if env, ok := volumeChownEnv("1000:1000", paths); ok || env != "" {
		t.Errorf("non-root path: got (%q, %v), want (\"\", false)", env, ok)
	}
	// no named volumes → nothing to inject (both root forms).
	if _, ok := volumeChownEnv("", nil); ok {
		t.Error("no chown paths should not inject MOAT_VOLUME_CHOWN")
	}
	if _, ok := volumeChownEnv("0:0", nil); ok {
		t.Error("0:0 with no chown paths should not inject MOAT_VOLUME_CHOWN")
	}
}
