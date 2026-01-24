package run

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/andybons/moat/internal/config"
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
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only ca.crt, got: %v", names)
	}
}
