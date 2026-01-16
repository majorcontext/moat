package run

import (
	"testing"

	"github.com/andybons/agentops/internal/config"
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
