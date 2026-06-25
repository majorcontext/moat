package cli

import "testing"

// TestShouldSkipVolumeRunForClean pins the `moat clean` data-loss guard and its
// separation from the prompt-skip flag: a volume-mode run with no extraction
// snapshot is skipped unless --force-volumes is set; bind-mode runs are never
// skipped; and a volume run with a snapshot is removed.
func TestShouldSkipVolumeRunForClean(t *testing.T) {
	cases := []struct {
		name         string
		forceVolumes bool
		mode         string
		hasSnapshot  bool
		want         bool
	}{
		{"bind run never skipped", false, "bind", false, false},
		{"empty mode (legacy bind) never skipped", false, "", false, false},
		{"volume, no snapshot -> skip", false, "volume", false, true},
		{"volume, has snapshot -> remove", false, "volume", true, false},
		{"volume, no snapshot, --force-volumes -> remove", true, "volume", false, false},
		{"bind, --force-volumes -> still removed (no-op for bind)", true, "bind", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldSkipVolumeRunForClean(c.forceVolumes, c.mode, c.hasSnapshot)
			if got != c.want {
				t.Errorf("shouldSkipVolumeRunForClean(%v, %q, %v) = %v, want %v",
					c.forceVolumes, c.mode, c.hasSnapshot, got, c.want)
			}
		})
	}
}
