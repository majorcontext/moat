package config

import "fmt"

// CheckVolumeRuntimeSupport rejects `type: volume` entries when the effective
// container runtime is Apple's `container` CLI, which has no named-volume support.
//
// It is called from both config load (when `runtime: apple` is explicit) and run
// setup (when the runtime is auto-detected) so the two validation points share one
// rule and error message and cannot drift.
func CheckVolumeRuntimeSupport(vols []VolumeConfig, appleRuntime bool) error {
	if !appleRuntime {
		return nil
	}
	for i, v := range vols {
		if v.Type == "volume" {
			return fmt.Errorf("volumes[%d]: named volumes (type: volume) are not supported on the Apple container runtime; use type: bind or a mounts: exclude: (tmpfs) instead", i)
		}
	}
	return nil
}
