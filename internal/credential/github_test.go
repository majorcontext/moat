package credential

import (
	"os"
	"testing"
)

func TestGitHubSetup_ConfigureProxy(t *testing.T) {
	setup := &GitHubSetup{}
	if setup.Provider() != ProviderGitHub {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), ProviderGitHub)
	}

	// Test that ConfigureProxy sets the correct headers
	mockProxy := &mockProxyConfigurer{credentials: make(map[string]string)}
	cred := &Credential{Token: "test-token"}

	setup.ConfigureProxy(mockProxy, cred)

	if mockProxy.credentials["api.github.com"] != "Bearer test-token" {
		t.Errorf("api.github.com credential = %q, want %q", mockProxy.credentials["api.github.com"], "Bearer test-token")
	}
	if mockProxy.credentials["github.com"] != "Bearer test-token" {
		t.Errorf("github.com credential = %q, want %q", mockProxy.credentials["github.com"], "Bearer test-token")
	}
}

func TestGitHubSetup_ContainerEnv(t *testing.T) {
	setup := &GitHubSetup{}
	cred := &Credential{Token: "test-token"}

	env := setup.ContainerEnv(cred)
	if len(env) != 1 {
		t.Fatalf("ContainerEnv() returned %d vars, want 1", len(env))
	}
	if env[0] != "GH_TOKEN=moat-proxy-injected" {
		t.Errorf("ContainerEnv()[0] = %q, want %q", env[0], "GH_TOKEN=moat-proxy-injected")
	}
}

func TestGitHubSetup_ContainerMounts(t *testing.T) {
	setup := &GitHubSetup{}
	cred := &Credential{Token: "test-token"}

	mounts, cleanupPath, err := setup.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Fatalf("ContainerMounts() error = %v", err)
	}

	// Behavior depends on whether user has ~/.config/gh/config.yml
	// If they do, we copy it. If not, no mounts are created.
	// Either way, the function should not error.

	if len(mounts) > 0 {
		// If mounts were created, verify the structure
		if mounts[0].Target != "/home/user/.config/gh" {
			t.Errorf("Mount target = %q, want %q", mounts[0].Target, "/home/user/.config/gh")
		}

		// Verify config.yml was created (not hosts.yml - auth is via GH_TOKEN)
		configPath := mounts[0].Source + "/config.yml"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			t.Error("config.yml was not created")
		}

		// Clean up
		setup.Cleanup(cleanupPath)

		// Verify cleanup worked
		if _, err := os.Stat(cleanupPath); !os.IsNotExist(err) {
			t.Error("Cleanup() did not remove the temp directory")
		}
	} else {
		// No mounts means user doesn't have gh config - that's fine
		if cleanupPath != "" {
			t.Errorf("cleanupPath should be empty when no mounts, got %q", cleanupPath)
		}
	}
}

func TestGitHubImpliedDeps(t *testing.T) {
	deps := GitHubImpliedDeps()
	if len(deps) != 2 {
		t.Fatalf("GitHubImpliedDeps() = %v, want [gh git]", deps)
	}
	if deps[0] != "gh" || deps[1] != "git" {
		t.Errorf("GitHubImpliedDeps() = %v, want [gh git]", deps)
	}
}
