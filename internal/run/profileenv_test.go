package run

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeGlobalConfig points MOAT_HOME at a temp dir containing the given
// config.yaml, so tests exercise the real loader rather than a stub.
func writeGlobalConfig(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatalf("writing config.yaml: %v", err)
		}
	}
	t.Setenv("MOAT_HOME", dir)
}

func TestProfileEnv(t *testing.T) {
	const cfg = `
profiles:
  lunaroute:
    env:
      ANTHROPIC_MODEL: glm-5.3
      ANTHROPIC_DEFAULT_HAIKU_MODEL: glm-5.3-flash
  work:
    env:
      ANTHROPIC_MODEL: claude-opus-5
  bare: {}
`

	t.Run("returns the profile's env, sorted", func(t *testing.T) {
		writeGlobalConfig(t, cfg)
		got := profileEnv("lunaroute", true)
		want := []string{
			"ANTHROPIC_DEFAULT_HAIKU_MODEL=glm-5.3-flash",
			"ANTHROPIC_MODEL=glm-5.3",
		}
		if !slices.Equal(got, want) {
			t.Errorf("profileEnv = %v, want %v", got, want)
		}
	})

	t.Run("profiles do not leak into each other", func(t *testing.T) {
		writeGlobalConfig(t, cfg)
		got := profileEnv("work", true)
		want := []string{"ANTHROPIC_MODEL=claude-opus-5"}
		if !slices.Equal(got, want) {
			t.Errorf("profileEnv = %v, want %v", got, want)
		}
	})

	// Companion cases to the above: every way of having nothing to contribute
	// must yield nothing, not a partial or a panic.
	t.Run("no profile selected", func(t *testing.T) {
		writeGlobalConfig(t, cfg)
		if got := profileEnv("", true); got != nil {
			t.Errorf("profileEnv(\"\") = %v, want nil — the default store is not a named profile", got)
		}
	})

	t.Run("profile not in the config", func(t *testing.T) {
		writeGlobalConfig(t, cfg)
		if got := profileEnv("unconfigured", true); got != nil {
			t.Errorf("profileEnv = %v, want nil", got)
		}
	})

	t.Run("profile with no env", func(t *testing.T) {
		writeGlobalConfig(t, cfg)
		if got := profileEnv("bare", true); got != nil {
			t.Errorf("profileEnv = %v, want nil", got)
		}
	})

	t.Run("no config file at all", func(t *testing.T) {
		writeGlobalConfig(t, "")
		if got := profileEnv("lunaroute", true); got != nil {
			t.Errorf("profileEnv = %v, want nil", got)
		}
	})

	t.Run("no profiles section", func(t *testing.T) {
		writeGlobalConfig(t, "proxy:\n  port: 8080\n")
		if got := profileEnv("lunaroute", true); got != nil {
			t.Errorf("profileEnv = %v, want nil", got)
		}
	})
}

// TestProfileEnvFiltersProxyVars covers the security property: profile env is
// user-writable configuration, so it must not be a way around network policy.
func TestProfileEnvFiltersProxyVars(t *testing.T) {
	const cfg = `
profiles:
  sneaky:
    env:
      HTTP_PROXY: http://attacker:8080
      https_proxy: http://attacker:8080
      NO_PROXY: "*"
      ALL_PROXY: socks5://attacker:1080
      MOAT_HOST_GATEWAY: attacker.example.com
      ANTHROPIC_MODEL: glm-5.3
`
	t.Run("filtered when the run has a proxy", func(t *testing.T) {
		writeGlobalConfig(t, cfg)
		got := profileEnv("sneaky", true)
		want := []string{"ANTHROPIC_MODEL=glm-5.3"}
		if !slices.Equal(got, want) {
			t.Errorf("profileEnv = %v, want only %v", got, want)
		}
	})

	// Companion case: with no proxy there is no policy to bypass, matching how
	// moat.yaml env and -e flags behave.
	t.Run("passed through when the run has no proxy", func(t *testing.T) {
		writeGlobalConfig(t, cfg)
		got := profileEnv("sneaky", false)
		if len(got) != 6 {
			t.Errorf("profileEnv returned %d vars (%v), want all 6 when no proxy is active", len(got), got)
		}
	})
}
