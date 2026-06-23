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
}
