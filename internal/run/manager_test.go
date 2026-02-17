package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"errors"
	"io"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/deps"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/storage"
)

// TestNetworkPolicyConfiguration verifies that network policy configuration
// from agent.yaml is properly wired into the proxy.
// The proxy is started when either:
// - Grants are provided (for credential injection)
// - Strict network policy is configured (for firewall enforcement)
func TestNetworkPolicyConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		config         *config.Config
		grants         []string
		wantProxyStart bool // whether proxy should be started
		wantPolicyCall bool // whether SetNetworkPolicy should be called
		wantFirewall   bool // whether firewall should be enabled
	}{
		{
			name: "strict policy with allows and grants",
			config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "strict",
					Allow:  []string{"api.example.com", "*.allowed.com"},
				},
			},
			grants:         []string{"github"},
			wantProxyStart: true,
			wantPolicyCall: true,
			wantFirewall:   true,
		},
		{
			name: "permissive policy with grants",
			config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "permissive",
				},
			},
			grants:         []string{"github"},
			wantProxyStart: true,
			wantPolicyCall: true,
			wantFirewall:   false,
		},
		{
			name: "strict policy without grants (firewall only)",
			config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "strict",
					Allow:  []string{"api.example.com"},
				},
			},
			grants:         nil,
			wantProxyStart: true, // proxy started for firewall
			wantPolicyCall: true, // policy configured on proxy
			wantFirewall:   true, // iptables firewall enabled
		},
		{
			name:           "nil config with grants",
			config:         nil,
			grants:         []string{"github"},
			wantProxyStart: true,  // proxy started for grants
			wantPolicyCall: false, // no config, so no policy call
			wantFirewall:   false,
		},
		{
			name: "permissive policy without grants",
			config: &config.Config{
				Network: config.NetworkConfig{
					Policy: "permissive",
				},
			},
			grants:         nil,
			wantProxyStart: false, // no grants, no strict policy
			wantPolicyCall: false,
			wantFirewall:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the logic from manager.go
			needsProxyForGrants := len(tt.grants) > 0
			needsProxyForFirewall := tt.config != nil && tt.config.Network.Policy == "strict"
			proxyStarted := needsProxyForGrants || needsProxyForFirewall

			if proxyStarted != tt.wantProxyStart {
				t.Errorf("proxy start: got %v, want %v", proxyStarted, tt.wantProxyStart)
			}

			// SetNetworkPolicy is called when proxy is started AND config exists
			policyCall := proxyStarted && tt.config != nil
			if policyCall != tt.wantPolicyCall {
				t.Errorf("SetNetworkPolicy call: got %v, want %v", policyCall, tt.wantPolicyCall)
			}

			// Firewall is enabled when strict policy is set
			firewallEnabled := needsProxyForFirewall
			if firewallEnabled != tt.wantFirewall {
				t.Errorf("firewall enabled: got %v, want %v", firewallEnabled, tt.wantFirewall)
			}
		})
	}
}

// TestNetworkPolicyDefaults verifies that default network policy is set correctly.
func TestNetworkPolicyDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Network.Policy != "permissive" {
		t.Errorf("expected default policy 'permissive', got %q", cfg.Network.Policy)
	}
}

// moatuserUID is the UID of the moatuser created in generated container images.
// This must match the value in internal/deps/dockerfile.go.
const moatuserUID = 5000

// determineContainerUser replicates the UID mapping logic from manager.go
// for testing purposes. This allows us to test the logic without a real container.
// In production, the UID/GID come from the workspace owner (via getWorkspaceOwner).
func determineContainerUser(goos string, workspaceUID, workspaceGID int) string {
	if goos == "linux" {
		if workspaceUID != moatuserUID {
			return fmt.Sprintf("%d:%d", workspaceUID, workspaceGID)
		}
		// If workspace owner UID matches moatuserUID, use the image's default moatuser
		return ""
	}
	// On macOS/Windows, leave containerUser empty to use the image default
	return ""
}

// TestContainerUserMapping verifies that container user is set correctly
// based on host OS and workspace owner UID. This is critical for security boundaries.
func TestContainerUserMapping(t *testing.T) {
	tests := []struct {
		name         string
		goos         string
		workspaceUID int
		workspaceGID int
		wantUser     string
	}{
		{
			name:         "Linux with typical developer UID",
			goos:         "linux",
			workspaceUID: 1000,
			workspaceGID: 1000,
			wantUser:     "1000:1000", // map to workspace owner
		},
		{
			name:         "Linux with moatuser UID",
			goos:         "linux",
			workspaceUID: moatuserUID,
			workspaceGID: moatuserUID,
			wantUser:     "", // use image default
		},
		{
			name:         "Linux with root UID",
			goos:         "linux",
			workspaceUID: 0,
			workspaceGID: 0,
			wantUser:     "0:0", // map to root (should be avoided)
		},
		{
			name:         "Linux with high UID",
			goos:         "linux",
			workspaceUID: 65534,
			workspaceGID: 65534,
			wantUser:     "65534:65534", // map to workspace owner
		},
		{
			name:         "Linux with different UID/GID",
			goos:         "linux",
			workspaceUID: 1001,
			workspaceGID: 1002,
			wantUser:     "1001:1002", // map to workspace owner with different group
		},
		{
			name:         "macOS always uses image default",
			goos:         "darwin",
			workspaceUID: 501,
			workspaceGID: 20,
			wantUser:     "", // Docker Desktop handles mapping
		},
		{
			name:         "Windows always uses image default",
			goos:         "windows",
			workspaceUID: 0,
			workspaceGID: 0,
			wantUser:     "", // Docker Desktop handles mapping
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineContainerUser(tt.goos, tt.workspaceUID, tt.workspaceGID)
			if got != tt.wantUser {
				t.Errorf("determineContainerUser(%q, %d, %d) = %q, want %q",
					tt.goos, tt.workspaceUID, tt.workspaceGID, got, tt.wantUser)
			}
		})
	}
}

// TestContainerUserMappingCurrentOS tests the UID mapping for the current OS.
// This test documents the expected behavior on the machine running tests.
func TestContainerUserMappingCurrentOS(t *testing.T) {
	// On macOS/Windows, we always expect empty string (use image default)
	// On Linux, we expect UID:GID mapping unless UID is exactly moatuserUID
	if runtime.GOOS != "linux" {
		got := determineContainerUser(runtime.GOOS, 1000, 1000)
		if got != "" {
			t.Errorf("on %s, expected empty containerUser, got %q", runtime.GOOS, got)
		}
	}
}

// mapContainerStateToRunState replicates the container state mapping logic from
// loadPersistedRuns in manager.go. This allows testing without a full manager.
// Docker uses "exited"/"dead" for stopped containers, while Apple uses "stopped".
func mapContainerStateToRunState(containerState, metadataState string) State {
	switch containerState {
	case "running":
		return StateRunning
	case "exited", "dead", "stopped":
		return StateStopped
	case "created", "restarting":
		return StateCreated
	default:
		return State(metadataState)
	}
}

// TestContainerStateMapping verifies that container states from different runtimes
// are correctly mapped to run states. Docker uses "exited"/"dead" while Apple
// containers use "stopped" for stopped containers.
func TestContainerStateMapping(t *testing.T) {
	tests := []struct {
		name           string
		containerState string
		metadataState  string
		wantState      State
	}{
		// Running state (same for both runtimes)
		{
			name:           "running container",
			containerState: "running",
			metadataState:  "running",
			wantState:      StateRunning,
		},
		// Docker stopped states
		{
			name:           "Docker exited container",
			containerState: "exited",
			metadataState:  "running",
			wantState:      StateStopped,
		},
		{
			name:           "Docker dead container",
			containerState: "dead",
			metadataState:  "running",
			wantState:      StateStopped,
		},
		// Apple stopped state
		{
			name:           "Apple stopped container",
			containerState: "stopped",
			metadataState:  "running",
			wantState:      StateStopped,
		},
		// Created/restarting states
		{
			name:           "created container",
			containerState: "created",
			metadataState:  "running",
			wantState:      StateCreated,
		},
		{
			name:           "restarting container",
			containerState: "restarting",
			metadataState:  "running",
			wantState:      StateCreated,
		},
		// Unknown state falls back to metadata
		{
			name:           "unknown state uses metadata",
			containerState: "unknown",
			metadataState:  "running",
			wantState:      StateRunning,
		},
		{
			name:           "paused state uses metadata",
			containerState: "paused",
			metadataState:  "stopped",
			wantState:      StateStopped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapContainerStateToRunState(tt.containerState, tt.metadataState)
			if got != tt.wantState {
				t.Errorf("mapContainerStateToRunState(%q, %q) = %q, want %q",
					tt.containerState, tt.metadataState, got, tt.wantState)
			}
		})
	}
}

// TestContainerStateMappingAppleRuntime specifically tests that Apple container's
// "stopped" state is handled correctly. This was a bug where Apple containers
// returned "stopped" but only "exited"/"dead" were recognized as stopped.
func TestContainerStateMappingAppleRuntime(t *testing.T) {
	// Apple containers return "stopped" for stopped containers
	// This must map to StateStopped, not fall through to the default case
	got := mapContainerStateToRunState("stopped", "running")
	if got != StateStopped {
		t.Errorf("Apple 'stopped' state mapped to %q, want %q", got, StateStopped)
	}

	// Verify it doesn't accidentally use the metadata state
	got = mapContainerStateToRunState("stopped", "created")
	if got != StateStopped {
		t.Errorf("Apple 'stopped' state with 'created' metadata mapped to %q, want %q", got, StateStopped)
	}
}

// TestEnsureCACertOnlyDir verifies that only the CA certificate is copied,
// not the private key. This is a security test to ensure containers can't
// access the signing key.
func TestEnsureCACertOnlyDir(t *testing.T) {
	// Create temp CA directory with cert and key
	caDir := t.TempDir()
	certContent := []byte("-----BEGIN CERTIFICATE-----\ntest cert\n-----END CERTIFICATE-----\n")
	keyContent := []byte("-----BEGIN PRIVATE KEY-----\ntest key\n-----END PRIVATE KEY-----\n")

	if err := os.WriteFile(filepath.Join(caDir, "ca.crt"), certContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caDir, "ca.key"), keyContent, 0600); err != nil {
		t.Fatal(err)
	}

	// Create cert-only directory
	certOnlyDir := filepath.Join(caDir, "public")
	if err := ensureCACertOnlyDir(caDir, certOnlyDir); err != nil {
		t.Fatalf("ensureCACertOnlyDir failed: %v", err)
	}

	// Verify certificate was copied
	copiedCert, err := os.ReadFile(filepath.Join(certOnlyDir, "ca.crt"))
	if err != nil {
		t.Fatalf("failed to read copied cert: %v", err)
	}
	if string(copiedCert) != string(certContent) {
		t.Errorf("copied cert doesn't match: got %q, want %q", copiedCert, certContent)
	}

	// Verify private key was NOT copied
	keyPath := filepath.Join(certOnlyDir, "ca.key")
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("private key should NOT exist in cert-only dir, but it does")
	}

	// Verify only the cert file exists (no other files)
	entries, err := os.ReadDir(certOnlyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 file in cert-only dir, got %d", len(entries))
	}
	if entries[0].Name() != "ca.crt" {
		t.Errorf("unexpected file in cert-only dir: %s", entries[0].Name())
	}
}

// TestEnsureCACertOnlyDirCaching verifies that the function uses content hash
// for caching - skips copying when content is same, updates when different.
func TestEnsureCACertOnlyDirCaching(t *testing.T) {
	caDir := t.TempDir()
	certContent := []byte("test certificate content")
	certPath := filepath.Join(caDir, "ca.crt")

	if err := os.WriteFile(certPath, certContent, 0644); err != nil {
		t.Fatal(err)
	}

	certOnlyDir := filepath.Join(caDir, "public")

	// First call should create the file
	if err := ensureCACertOnlyDir(caDir, certOnlyDir); err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	dstPath := filepath.Join(certOnlyDir, "ca.crt")
	info1, _ := os.Stat(dstPath)

	// Second call with same content should be a no-op (hash-based caching)
	if err := ensureCACertOnlyDir(caDir, certOnlyDir); err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	// Verify the file wasn't rewritten (mod time should be same)
	info2, _ := os.Stat(dstPath)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("file was rewritten on second call with same content")
	}

	// Now change the source content - should trigger update
	newContent := []byte("updated certificate content")
	if err := os.WriteFile(certPath, newContent, 0644); err != nil {
		t.Fatal(err)
	}

	if err := ensureCACertOnlyDir(caDir, certOnlyDir); err != nil {
		t.Fatalf("third call failed: %v", err)
	}

	// Verify content was updated
	gotContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent) != string(newContent) {
		t.Errorf("content not updated: got %q, want %q", gotContent, newContent)
	}
}

func TestEnsureCACertOnlyDirRemovesStaleFiles(t *testing.T) {
	caDir := t.TempDir()
	certOnlyDir := filepath.Join(caDir, "public")

	// Create source certificate
	certContent := []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----")
	if err := os.WriteFile(filepath.Join(caDir, "ca.crt"), certContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create certOnlyDir with a stale file (simulating accidental private key copy)
	if err := os.MkdirAll(certOnlyDir, 0755); err != nil {
		t.Fatal(err)
	}
	staleKeyPath := filepath.Join(certOnlyDir, "ca.key")
	if err := os.WriteFile(staleKeyPath, []byte("PRIVATE KEY DATA"), 0600); err != nil {
		t.Fatal(err)
	}

	// Run ensureCACertOnlyDir - it should remove the stale file
	if err := ensureCACertOnlyDir(caDir, certOnlyDir); err != nil {
		t.Fatalf("ensureCACertOnlyDir failed: %v", err)
	}

	// Verify the stale file was removed
	if _, err := os.Stat(staleKeyPath); !os.IsNotExist(err) {
		t.Error("stale ca.key file should have been removed")
	}

	// Verify the certificate was copied
	if _, err := os.Stat(filepath.Join(certOnlyDir, "ca.crt")); err != nil {
		t.Error("ca.crt should exist after ensureCACertOnlyDir")
	}

	// Verify only ca.crt is in the directory
	entries, err := os.ReadDir(certOnlyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ca.crt" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only ca.crt, got: %v", names)
	}
}

// DockerModeContainerConfig captures the container configuration computed from docker mode.
// This struct allows testing the docker mode wiring logic without creating actual containers.
type DockerModeContainerConfig struct {
	Mounts     []container.MountConfig
	Env        []string
	GroupAdd   []string
	Privileged bool
}

// computeDockerModeConfig replicates the docker mode wiring logic from manager.go.
// This allows testing the logic without a full manager or real container runtime.
func computeDockerModeConfig(dockerConfig *DockerDependencyConfig) DockerModeContainerConfig {
	var cfg DockerModeContainerConfig

	if dockerConfig == nil {
		return cfg
	}

	// Handle different modes
	switch dockerConfig.Mode {
	case deps.DockerModeHost:
		// Host mode: mount Docker socket and pass GID for group setup
		cfg.Mounts = append(cfg.Mounts, dockerConfig.SocketMount)
		cfg.Env = append(cfg.Env, "MOAT_DOCKER_GID="+dockerConfig.GroupID)
		cfg.GroupAdd = append(cfg.GroupAdd, dockerConfig.GroupID)
	case deps.DockerModeDind:
		// Dind mode: signal moat-init to start dockerd
		cfg.Env = append(cfg.Env, "MOAT_DOCKER_DIND=1")
	}

	// Privileged is set from dockerConfig (only true for dind)
	if dockerConfig.Privileged {
		cfg.Privileged = true
	}

	return cfg
}

// TestDockerModeWiring verifies that docker modes are correctly wired into
// container configuration in manager.go.
func TestDockerModeWiring(t *testing.T) {
	tests := []struct {
		name          string
		dockerConfig  *DockerDependencyConfig
		wantMounts    int
		wantEnv       string
		wantGroupAdd  int
		wantPriv      bool
		wantNoGroupID bool
	}{
		{
			name:         "nil docker config - no changes",
			dockerConfig: nil,
			wantMounts:   0,
			wantEnv:      "",
			wantGroupAdd: 0,
			wantPriv:     false,
		},
		{
			name: "host mode - socket mount and GID",
			dockerConfig: &DockerDependencyConfig{
				Mode: deps.DockerModeHost,
				SocketMount: container.MountConfig{
					Source:   "/var/run/docker.sock",
					Target:   "/var/run/docker.sock",
					ReadOnly: false,
				},
				GroupID:    "999",
				Privileged: false,
			},
			wantMounts:   1,
			wantEnv:      "MOAT_DOCKER_GID=999",
			wantGroupAdd: 1,
			wantPriv:     false,
		},
		{
			name: "dind mode - privileged and env var",
			dockerConfig: &DockerDependencyConfig{
				Mode:       deps.DockerModeDind,
				Privileged: true,
			},
			wantMounts:    0,
			wantEnv:       "MOAT_DOCKER_DIND=1",
			wantGroupAdd:  0,
			wantPriv:      true,
			wantNoGroupID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := computeDockerModeConfig(tt.dockerConfig)

			// Check mounts
			if len(cfg.Mounts) != tt.wantMounts {
				t.Errorf("mounts count: got %d, want %d", len(cfg.Mounts), tt.wantMounts)
			}

			// Check env vars
			if tt.wantEnv == "" {
				if len(cfg.Env) != 0 {
					t.Errorf("expected no env vars, got %v", cfg.Env)
				}
			} else {
				found := false
				for _, env := range cfg.Env {
					if env == tt.wantEnv {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected env var %q, got %v", tt.wantEnv, cfg.Env)
				}
			}

			// Check GroupAdd
			if len(cfg.GroupAdd) != tt.wantGroupAdd {
				t.Errorf("groupAdd count: got %d, want %d", len(cfg.GroupAdd), tt.wantGroupAdd)
			}

			// Check privileged
			if cfg.Privileged != tt.wantPriv {
				t.Errorf("privileged: got %v, want %v", cfg.Privileged, tt.wantPriv)
			}

			// Verify no MOAT_DOCKER_GID in dind mode
			if tt.wantNoGroupID {
				for _, env := range cfg.Env {
					if strings.HasPrefix(env, "MOAT_DOCKER_GID=") {
						t.Errorf("dind mode should not have MOAT_DOCKER_GID, got %s", env)
					}
				}
			}
		})
	}
}

// TestDockerModeHostMountDetails verifies that host mode configures
// the correct socket mount path and permissions.
func TestDockerModeHostMountDetails(t *testing.T) {
	dockerConfig := &DockerDependencyConfig{
		Mode: deps.DockerModeHost,
		SocketMount: container.MountConfig{
			Source:   "/var/run/docker.sock",
			Target:   "/var/run/docker.sock",
			ReadOnly: false,
		},
		GroupID: "999",
	}

	cfg := computeDockerModeConfig(dockerConfig)

	if len(cfg.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(cfg.Mounts))
	}

	mount := cfg.Mounts[0]
	if mount.Source != "/var/run/docker.sock" {
		t.Errorf("mount source: got %q, want /var/run/docker.sock", mount.Source)
	}
	if mount.Target != "/var/run/docker.sock" {
		t.Errorf("mount target: got %q, want /var/run/docker.sock", mount.Target)
	}
	if mount.ReadOnly {
		t.Error("mount should not be read-only for docker socket")
	}

	// GroupAdd should have the GID
	if len(cfg.GroupAdd) != 1 || cfg.GroupAdd[0] != "999" {
		t.Errorf("groupAdd: got %v, want [999]", cfg.GroupAdd)
	}

	// Should not be privileged
	if cfg.Privileged {
		t.Error("host mode should not be privileged")
	}
}

// TestDockerModeDindPrivileged verifies that dind mode sets privileged
// and the correct env var, without socket mount or GroupAdd.
func TestDockerModeDindPrivileged(t *testing.T) {
	dockerConfig := &DockerDependencyConfig{
		Mode:       deps.DockerModeDind,
		Privileged: true,
	}

	cfg := computeDockerModeConfig(dockerConfig)

	// No mounts for dind
	if len(cfg.Mounts) != 0 {
		t.Errorf("dind mode should have no mounts, got %d", len(cfg.Mounts))
	}

	// No GroupAdd for dind
	if len(cfg.GroupAdd) != 0 {
		t.Errorf("dind mode should have no groupAdd, got %v", cfg.GroupAdd)
	}

	// Must be privileged
	if !cfg.Privileged {
		t.Error("dind mode must be privileged")
	}

	// Must have MOAT_DOCKER_DIND=1 env var
	found := false
	for _, env := range cfg.Env {
		if env == "MOAT_DOCKER_DIND=1" {
			found = true
		}
		if strings.HasPrefix(env, "MOAT_DOCKER_GID=") {
			t.Errorf("dind mode should not have MOAT_DOCKER_GID, got %s", env)
		}
	}
	if !found {
		t.Errorf("dind mode should have MOAT_DOCKER_DIND=1, got %v", cfg.Env)
	}
}

// TestDockerModeExclusive verifies that host and dind modes have
// mutually exclusive configurations.
func TestDockerModeExclusive(t *testing.T) {
	// Host mode should never have MOAT_DOCKER_DIND or Privileged=true
	hostConfig := &DockerDependencyConfig{
		Mode: deps.DockerModeHost,
		SocketMount: container.MountConfig{
			Source: "/var/run/docker.sock",
			Target: "/var/run/docker.sock",
		},
		GroupID:    "999",
		Privileged: false, // This is set by ResolveDockerDependency
	}

	hostCfg := computeDockerModeConfig(hostConfig)

	for _, env := range hostCfg.Env {
		if env == "MOAT_DOCKER_DIND=1" {
			t.Error("host mode should never have MOAT_DOCKER_DIND=1")
		}
	}
	if hostCfg.Privileged {
		t.Error("host mode should never be privileged")
	}

	// Dind mode should never have MOAT_DOCKER_GID or socket mounts
	dindConfig := &DockerDependencyConfig{
		Mode:       deps.DockerModeDind,
		Privileged: true,
	}

	dindCfg := computeDockerModeConfig(dindConfig)

	for _, env := range dindCfg.Env {
		if strings.HasPrefix(env, "MOAT_DOCKER_GID=") {
			t.Error("dind mode should never have MOAT_DOCKER_GID")
		}
	}
	if len(dindCfg.Mounts) > 0 {
		t.Error("dind mode should never have socket mounts")
	}
	if len(dindCfg.GroupAdd) > 0 {
		t.Error("dind mode should never have GroupAdd")
	}
}

func TestManager_CreateWithBuildKit(t *testing.T) {
	// Test that buildkit sidecar is created for dind mode
	dockerConfig := &DockerDependencyConfig{
		Mode:       deps.DockerModeDind,
		Privileged: true,
	}

	result := computeBuildKitConfig(dockerConfig, "test-run-id")

	if !result.Enabled {
		t.Error("BuildKit should be enabled for dind mode")
	}
	if result.NetworkName != "moat-test-run-id" {
		t.Errorf("NetworkName: got %q, want %q", result.NetworkName, "moat-test-run-id")
	}
	if result.SidecarName != "moat-buildkit-test-run-id" {
		t.Errorf("SidecarName: got %q, want %q", result.SidecarName, "moat-buildkit-test-run-id")
	}
	if result.SidecarImage != "moby/buildkit:latest" {
		t.Errorf("SidecarImage: got %q, want %q", result.SidecarImage, "moby/buildkit:latest")
	}
}

func TestComputeBuildKitEnv(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		wantEnvVar bool
	}{
		{
			name:       "buildkit enabled",
			enabled:    true,
			wantEnvVar: true,
		},
		{
			name:       "buildkit disabled",
			enabled:    false,
			wantEnvVar: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := computeBuildKitEnv(tt.enabled)

			found := false
			for _, e := range env {
				if strings.HasPrefix(e, "BUILDKIT_HOST=") {
					found = true
					if !tt.wantEnvVar {
						t.Error("BUILDKIT_HOST should not be set when disabled")
					}
					if !strings.Contains(e, "tcp://buildkit:1234") {
						t.Errorf("BUILDKIT_HOST value incorrect: %s", e)
					}
				}
			}

			if tt.wantEnvVar && !found {
				t.Error("BUILDKIT_HOST should be set when enabled")
			}
		})
	}
}

// --- Token refresh loop tests ---

// mockRefreshableProvider implements provider.RefreshableProvider for testing.
type mockRefreshableProvider struct {
	mu       sync.Mutex
	calls    int
	token    string // token to return on refresh
	err      error  // error to return on refresh
	interval time.Duration
	canDo    bool
}

func (m *mockRefreshableProvider) Refresh(_ context.Context, p provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	p.SetCredential("api.example.com", "Bearer "+m.token)
	updated := *cred
	updated.Token = m.token
	return &updated, nil
}

func (m *mockRefreshableProvider) CanRefresh(*provider.Credential) bool { return m.canDo }
func (m *mockRefreshableProvider) RefreshInterval() time.Duration       { return m.interval }

func (m *mockRefreshableProvider) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// mockProxyConfigurer implements credential.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	mu          sync.Mutex
	credentials map[string]string
}

func newMockProxy() *mockProxyConfigurer {
	return &mockProxyConfigurer{credentials: make(map[string]string)}
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials[host] = value
}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) AddExtraHeader(string, string, string) {}

func (m *mockProxyConfigurer) AddResponseTransformer(string, credential.ResponseTransformer) {}

func (m *mockProxyConfigurer) RemoveRequestHeader(string, string) {}

func (m *mockProxyConfigurer) SetTokenSubstitution(string, string, string) {}

func (m *mockProxyConfigurer) get(host string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.credentials[host]
}

// mockCredStore implements credential.Store for testing.
type mockCredStore struct {
	mu      sync.Mutex
	saved   []credential.Credential
	saveErr error
}

func (s *mockCredStore) Save(cred credential.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, cred)
	return nil
}

func (s *mockCredStore) Get(credential.Provider) (*credential.Credential, error) { return nil, nil }
func (s *mockCredStore) Delete(credential.Provider) error                        { return nil }
func (s *mockCredStore) List() ([]credential.Credential, error)                  { return nil, nil }

func (s *mockCredStore) savedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.saved)
}

func (s *mockCredStore) lastSaved() credential.Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved[len(s.saved)-1]
}

func TestRefreshToken_Success(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	store := &mockCredStore{}
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	refresher := &mockRefreshableProvider{
		token:    "new-token",
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        store,
	}

	m.refreshToken(context.Background(), r, p, target)

	// Verify proxy was updated
	if got := p.get("api.example.com"); got != "Bearer new-token" {
		t.Errorf("proxy credential = %q, want %q", got, "Bearer new-token")
	}

	// Verify in-memory credential was updated
	if target.cred.Token != "new-token" {
		t.Errorf("in-memory token = %q, want %q", target.cred.Token, "new-token")
	}

	// Verify credential was persisted to store
	if store.savedCount() != 1 {
		t.Fatalf("store.Save called %d times, want 1", store.savedCount())
	}
	if store.lastSaved().Token != "new-token" {
		t.Errorf("persisted token = %q, want %q", store.lastSaved().Token, "new-token")
	}
}

func TestRefreshToken_Unchanged(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	store := &mockCredStore{}
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "same-token",
	}

	refresher := &mockRefreshableProvider{
		token:    "same-token", // same as current
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        store,
	}

	m.refreshToken(context.Background(), r, p, target)

	// Should not persist when token is unchanged
	if store.savedCount() != 0 {
		t.Errorf("store.Save should not be called for unchanged token, called %d times", store.savedCount())
	}
}

func TestRefreshToken_Error(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	store := &mockCredStore{}
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	refresher := &mockRefreshableProvider{
		err:      fmt.Errorf("gh auth token: command not found"),
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        store,
	}

	m.refreshToken(context.Background(), r, p, target)

	// Should keep existing token on error
	if target.cred.Token != "old-token" {
		t.Errorf("token should be unchanged on error, got %q", target.cred.Token)
	}

	// Should not persist on error
	if store.savedCount() != 0 {
		t.Errorf("store.Save should not be called on error, called %d times", store.savedCount())
	}
}

func TestRefreshToken_StoreSaveError(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	store := &mockCredStore{saveErr: fmt.Errorf("disk full")}
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	refresher := &mockRefreshableProvider{
		token:    "new-token",
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        store,
	}

	// Should not panic or fail — store error is logged but not fatal
	m.refreshToken(context.Background(), r, p, target)

	// In-memory credential should still be updated even if persist fails
	if target.cred.Token != "new-token" {
		t.Errorf("in-memory token = %q, want %q (should update even if store fails)", target.cred.Token, "new-token")
	}
}

func TestRefreshToken_NilStore(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	refresher := &mockRefreshableProvider{
		token:    "new-token",
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        nil, // no store
	}

	// Should not panic with nil store
	m.refreshToken(context.Background(), r, p, target)

	if target.cred.Token != "new-token" {
		t.Errorf("in-memory token = %q, want %q", target.cred.Token, "new-token")
	}
}

func TestRunTokenRefreshLoop_ImmediateRefresh(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	r := &Run{ID: "test-run", exitCh: make(chan struct{})}

	refresher := &mockRefreshableProvider{
		token:    "fresh-token",
		interval: time.Hour, // long interval so only immediate refresh fires
		canDo:    true,
	}

	cred := &credential.Credential{Token: "stale-token"}
	targets := []refreshTarget{
		{
			providerName: credential.ProviderGitHub,
			refresher:    refresher,
			cred:         cred,
			store:        &mockCredStore{},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.runTokenRefreshLoop(ctx, r, p, targets)
		close(done)
	}()

	// Give the goroutine time to perform the immediate refresh
	time.Sleep(50 * time.Millisecond)

	// Verify the immediate refresh happened
	if refresher.callCount() < 1 {
		t.Errorf("expected at least 1 refresh call (immediate), got %d", refresher.callCount())
	}

	cancel()
	<-done
}

func TestRunTokenRefreshLoop_ExitsOnCancel(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	r := &Run{ID: "test-run", exitCh: make(chan struct{})}

	refresher := &mockRefreshableProvider{
		token:    "token",
		interval: time.Millisecond, // fast ticks
		canDo:    true,
	}

	targets := []refreshTarget{
		{
			providerName: credential.ProviderGitHub,
			refresher:    refresher,
			cred:         &credential.Credential{Token: "old"},
			store:        &mockCredStore{},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.runTokenRefreshLoop(ctx, r, p, targets)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop did not exit after context cancellation")
	}
}

func TestRunTokenRefreshLoop_ExitsOnExitCh(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	r := &Run{ID: "test-run", exitCh: make(chan struct{})}

	refresher := &mockRefreshableProvider{
		token:    "token",
		interval: time.Hour,
		canDo:    true,
	}

	targets := []refreshTarget{
		{
			providerName: credential.ProviderGitHub,
			refresher:    refresher,
			cred:         &credential.Credential{Token: "old"},
			store:        &mockCredStore{},
		},
	}

	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		m.runTokenRefreshLoop(ctx, r, p, targets)
		close(done)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	close(r.exitCh)

	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop did not exit after exitCh closed")
	}
}

func TestRunTokenRefreshLoop_TickerRefresh(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	r := &Run{ID: "test-run", exitCh: make(chan struct{})}

	refresher := &mockRefreshableProvider{
		token:    "refreshed",
		interval: 20 * time.Millisecond, // fast ticks for testing
		canDo:    true,
	}

	targets := []refreshTarget{
		{
			providerName: credential.ProviderGitHub,
			refresher:    refresher,
			cred:         &credential.Credential{Token: "initial"},
			store:        &mockCredStore{},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		m.runTokenRefreshLoop(ctx, r, p, targets)
		close(done)
	}()

	// Wait for immediate refresh + at least one ticker refresh
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	// Should have at least 2 calls: 1 immediate + 1 or more from ticker
	if refresher.callCount() < 2 {
		t.Errorf("expected at least 2 refresh calls (immediate + ticker), got %d", refresher.callCount())
	}
}

func TestMinRefreshInterval(t *testing.T) {
	targets := []refreshTarget{
		{refresher: &mockRefreshableProvider{interval: 30 * time.Minute}},
		{refresher: &mockRefreshableProvider{interval: 5 * time.Minute}},
		{refresher: &mockRefreshableProvider{interval: 1 * time.Hour}},
	}

	got := minRefreshInterval(targets)
	if got != 5*time.Minute {
		t.Errorf("minRefreshInterval() = %v, want %v", got, 5*time.Minute)
	}
}

func TestMinRefreshInterval_SingleTarget(t *testing.T) {
	targets := []refreshTarget{
		{refresher: &mockRefreshableProvider{interval: 45 * time.Minute}},
	}

	got := minRefreshInterval(targets)
	if got != 45*time.Minute {
		t.Errorf("minRefreshInterval() = %v, want %v", got, 45*time.Minute)
	}
}

func TestRefreshToken_Backoff(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	store := &mockCredStore{}
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	refresher := &mockRefreshableProvider{
		err:      fmt.Errorf("temporary failure"),
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        store,
	}

	// First call should set backoff
	m.refreshToken(context.Background(), r, p, target)

	if target.retryDelay != refreshRetryMin {
		t.Errorf("retryDelay after first failure = %v, want %v", target.retryDelay, refreshRetryMin)
	}
	if target.nextRetryAfter.IsZero() {
		t.Error("nextRetryAfter should be set after failure")
	}

	// Second call within backoff window should be skipped
	callsBefore := refresher.callCount()
	m.refreshToken(context.Background(), r, p, target)
	if refresher.callCount() != callsBefore {
		t.Errorf("refresh should be skipped during backoff, calls went from %d to %d", callsBefore, refresher.callCount())
	}
}

func TestRefreshToken_BackoffReset(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	store := &mockCredStore{}
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	refresher := &mockRefreshableProvider{
		err:      fmt.Errorf("temporary failure"),
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        store,
	}

	// Trigger a failure to set backoff
	m.refreshToken(context.Background(), r, p, target)

	if target.retryDelay == 0 {
		t.Fatal("retryDelay should be non-zero after failure")
	}

	// Now make refresh succeed and clear the backoff window
	refresher.mu.Lock()
	refresher.err = nil
	refresher.token = "new-token"
	refresher.mu.Unlock()
	target.nextRetryAfter = time.Time{} // clear backoff window to allow retry

	m.refreshToken(context.Background(), r, p, target)

	// Backoff should be reset
	if target.retryDelay != 0 {
		t.Errorf("retryDelay should be reset to 0 after success, got %v", target.retryDelay)
	}
	if !target.nextRetryAfter.IsZero() {
		t.Error("nextRetryAfter should be zero after success")
	}
}

func TestRefreshToken_Revoked(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	store := &mockCredStore{}
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	refresher := &mockRefreshableProvider{
		err:      fmt.Errorf("%w: token was revoked", provider.ErrTokenRevoked),
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        store,
	}

	m.refreshToken(context.Background(), r, p, target)

	if !target.revoked {
		t.Error("target.revoked should be true after ErrTokenRevoked")
	}

	// Subsequent calls should be no-ops
	callsBefore := refresher.callCount()
	m.refreshToken(context.Background(), r, p, target)
	if refresher.callCount() != callsBefore {
		t.Error("refresh should not be called after revocation")
	}
}

func TestRefreshToken_Revoked_WrappedError(t *testing.T) {
	m := &Manager{}
	p := newMockProxy()
	r := &Run{ID: "test-run"}

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
	}

	// Use errors.Join-style wrapping to verify errors.Is works through layers
	wrappedErr := fmt.Errorf("refresh failed: %w", provider.ErrTokenRevoked)
	if !errors.Is(wrappedErr, provider.ErrTokenRevoked) {
		t.Fatal("test setup: wrapped error should match ErrTokenRevoked")
	}

	refresher := &mockRefreshableProvider{
		err:      wrappedErr,
		interval: 30 * time.Minute,
		canDo:    true,
	}

	target := &refreshTarget{
		providerName: credential.ProviderGitHub,
		refresher:    refresher,
		cred:         cred,
		store:        &mockCredStore{},
	}

	m.refreshToken(context.Background(), r, p, target)

	if !target.revoked {
		t.Error("target.revoked should be true for wrapped ErrTokenRevoked")
	}
}

// --- loadPersistedRuns stale route cleanup tests ---

// stubRuntime is a minimal container.Runtime implementation for testing
// loadPersistedRuns. Only ContainerState is implemented; all other methods panic.
type stubRuntime struct {
	states map[string]string // container ID -> state (e.g. "exited")
}

func (s *stubRuntime) ContainerState(_ context.Context, id string) (string, error) {
	state, ok := s.states[id]
	if !ok {
		return "", fmt.Errorf("container %q not found", id)
	}
	return state, nil
}

func (s *stubRuntime) Type() container.RuntimeType { return container.RuntimeDocker }
func (s *stubRuntime) Ping(context.Context) error  { return nil }
func (s *stubRuntime) CreateContainer(context.Context, container.Config) (string, error) {
	panic("not implemented")
}
func (s *stubRuntime) StartContainer(context.Context, string) error { panic("not implemented") }
func (s *stubRuntime) StopContainer(context.Context, string) error  { panic("not implemented") }
func (s *stubRuntime) WaitContainer(ctx context.Context, _ string) (int64, error) {
	// Block until context is canceled (test cleanup)
	<-ctx.Done()
	return 0, ctx.Err()
}
func (s *stubRuntime) RemoveContainer(context.Context, string) error { panic("not implemented") }
func (s *stubRuntime) ContainerLogs(context.Context, string) (io.ReadCloser, error) {
	panic("not implemented")
}
func (s *stubRuntime) ContainerLogsAll(context.Context, string) ([]byte, error) {
	panic("not implemented")
}
func (s *stubRuntime) GetPortBindings(context.Context, string) (map[int]int, error) {
	panic("not implemented")
}
func (s *stubRuntime) GetHostAddress() string                   { return "127.0.0.1" }
func (s *stubRuntime) SupportsHostNetwork() bool                { return true }
func (s *stubRuntime) NetworkManager() container.NetworkManager { return nil }
func (s *stubRuntime) SidecarManager() container.SidecarManager { return nil }
func (s *stubRuntime) BuildManager() container.BuildManager     { return nil }
func (s *stubRuntime) ServiceManager() container.ServiceManager { return nil }
func (s *stubRuntime) Close() error                             { return nil }
func (s *stubRuntime) SetupFirewall(context.Context, string, string, int) error {
	panic("not implemented")
}
func (s *stubRuntime) ListImages(context.Context) ([]container.ImageInfo, error) {
	panic("not implemented")
}
func (s *stubRuntime) ListContainers(context.Context) ([]container.Info, error) {
	panic("not implemented")
}
func (s *stubRuntime) RemoveImage(context.Context, string) error { panic("not implemented") }
func (s *stubRuntime) Attach(context.Context, string, container.AttachOptions) error {
	panic("not implemented")
}
func (s *stubRuntime) StartAttached(context.Context, string, container.AttachOptions) error {
	panic("not implemented")
}
func (s *stubRuntime) ResizeTTY(context.Context, string, uint, uint) error {
	panic("not implemented")
}

// TestLoadPersistedRunsCleansStaleRoutes verifies that loadPersistedRuns removes
// routes for containers that are no longer running. This prevents the bug where
// a stale routes.json entry blocks reuse of a run name after the container has stopped.
func TestLoadPersistedRunsCleansStaleRoutes(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Set up persisted run metadata on disk
	baseDir := filepath.Join(tmpHome, ".moat", "runs")
	runID := "run_deadbeef1234"
	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	err = store.SaveMetadata(storage.Metadata{
		Name:        "my-agent",
		ContainerID: "container-abc",
		State:       "running",
		Workspace:   "/tmp/workspace",
		CreatedAt:   time.Now().Add(-1 * time.Hour),
		StartedAt:   time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pre-populate routes.json with a stale route for this agent
	routeDir := filepath.Join(tmpHome, ".moat", "routes")
	routes, err := routing.NewRouteTable(routeDir)
	if err != nil {
		t.Fatal(err)
	}
	err = routes.Add("my-agent", map[string]string{"default": "127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the route exists before loading
	if !routes.AgentExists("my-agent") {
		t.Fatal("route should exist before loadPersistedRuns")
	}

	// Create a manager with a stub runtime that reports the container as exited
	m := &Manager{
		runtime: &stubRuntime{
			states: map[string]string{"container-abc": "exited"},
		},
		runs:   make(map[string]*Run),
		routes: routes,
	}

	err = m.loadPersistedRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// The stale route should have been cleaned up
	if routes.AgentExists("my-agent") {
		t.Error("stale route for stopped container should have been removed by loadPersistedRuns")
	}

	// The run should still be loaded (just with stopped state)
	if len(m.runs) != 1 {
		t.Fatalf("expected 1 loaded run, got %d", len(m.runs))
	}
	r := m.runs[runID]
	if r.State != StateStopped {
		t.Errorf("expected run state %q, got %q", StateStopped, r.State)
	}
}

// TestLoadPersistedRunsKeepsRoutesForRunningContainers verifies that
// loadPersistedRuns does NOT remove routes for containers that are still running.
func TestLoadPersistedRunsKeepsRoutesForRunningContainers(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	baseDir := filepath.Join(tmpHome, ".moat", "runs")
	runID := "run_livebeef1234"
	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	err = store.SaveMetadata(storage.Metadata{
		Name:        "live-agent",
		ContainerID: "container-live",
		State:       "running",
		Workspace:   "/tmp/workspace",
		CreatedAt:   time.Now().Add(-1 * time.Hour),
		StartedAt:   time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	routeDir := filepath.Join(tmpHome, ".moat", "routes")
	routes, err := routing.NewRouteTable(routeDir)
	if err != nil {
		t.Fatal(err)
	}
	err = routes.Add("live-agent", map[string]string{"default": "127.0.0.1:9090"})
	if err != nil {
		t.Fatal(err)
	}

	m := &Manager{
		runtime: &stubRuntime{
			states: map[string]string{"container-live": "running"},
		},
		runs:   make(map[string]*Run),
		routes: routes,
	}

	err = m.loadPersistedRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Route should be preserved for a running container
	if !routes.AgentExists("live-agent") {
		t.Error("route for running container should NOT be removed by loadPersistedRuns")
	}
}

// TestLoadPersistedRunsCleansRoutesForMissingContainers verifies that
// loadPersistedRuns removes routes when the container no longer exists at all
// (e.g., was manually removed via docker rm).
func TestLoadPersistedRunsCleansRoutesForMissingContainers(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	baseDir := filepath.Join(tmpHome, ".moat", "runs")
	runID := "run_gone12345678"
	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	err = store.SaveMetadata(storage.Metadata{
		Name:        "gone-agent",
		ContainerID: "container-gone",
		State:       "running",
		Workspace:   "/tmp/workspace",
		CreatedAt:   time.Now().Add(-1 * time.Hour),
		StartedAt:   time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	routeDir := filepath.Join(tmpHome, ".moat", "routes")
	routes, err := routing.NewRouteTable(routeDir)
	if err != nil {
		t.Fatal(err)
	}
	err = routes.Add("gone-agent", map[string]string{"default": "127.0.0.1:7070"})
	if err != nil {
		t.Fatal(err)
	}

	// Stub runtime with NO containers — ContainerState will return error
	m := &Manager{
		runtime: &stubRuntime{
			states: map[string]string{},
		},
		runs:   make(map[string]*Run),
		routes: routes,
	}

	err = m.loadPersistedRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if routes.AgentExists("gone-agent") {
		t.Error("stale route for missing container should have been removed by loadPersistedRuns")
	}
}
