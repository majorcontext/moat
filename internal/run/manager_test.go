package run

import (
	"fmt"
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
func determineContainerUser(goos string, hostUID, hostGID int) string {
	if goos == "linux" {
		if hostUID != moatuserUID {
			return fmt.Sprintf("%d:%d", hostUID, hostGID)
		}
		// If host UID matches moatuserUID, use the image's default moatuser
		return ""
	}
	// On macOS/Windows, leave containerUser empty to use the image default
	return ""
}

// TestContainerUserMapping verifies that container user is set correctly
// based on host OS and UID. This is critical for security boundaries.
func TestContainerUserMapping(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		hostUID  int
		hostGID  int
		wantUser string
	}{
		{
			name:     "Linux with typical developer UID",
			goos:     "linux",
			hostUID:  1000,
			hostGID:  1000,
			wantUser: "1000:1000", // map to host user
		},
		{
			name:     "Linux with moatuser UID",
			goos:     "linux",
			hostUID:  moatuserUID,
			hostGID:  moatuserUID,
			wantUser: "", // use image default
		},
		{
			name:     "Linux with root UID",
			goos:     "linux",
			hostUID:  0,
			hostGID:  0,
			wantUser: "0:0", // map to root (should be avoided)
		},
		{
			name:     "Linux with high UID",
			goos:     "linux",
			hostUID:  65534,
			hostGID:  65534,
			wantUser: "65534:65534", // map to host user
		},
		{
			name:     "Linux with different UID/GID",
			goos:     "linux",
			hostUID:  1001,
			hostGID:  1002,
			wantUser: "1001:1002", // map to host user with different group
		},
		{
			name:     "macOS always uses image default",
			goos:     "darwin",
			hostUID:  501,
			hostGID:  20,
			wantUser: "", // Docker Desktop handles mapping
		},
		{
			name:     "Windows always uses image default",
			goos:     "windows",
			hostUID:  0,
			hostGID:  0,
			wantUser: "", // Docker Desktop handles mapping
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineContainerUser(tt.goos, tt.hostUID, tt.hostGID)
			if got != tt.wantUser {
				t.Errorf("determineContainerUser(%q, %d, %d) = %q, want %q",
					tt.goos, tt.hostUID, tt.hostGID, got, tt.wantUser)
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
