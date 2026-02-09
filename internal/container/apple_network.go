package container

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// appleNetworkManager implements NetworkManager using the Apple container CLI.
type appleNetworkManager struct {
	containerBin string
}

// CreateNetwork creates an Apple container network.
// Returns the network name as the identifier.
func (m *appleNetworkManager) CreateNetwork(ctx context.Context, name string) (string, error) {
	cmd := exec.CommandContext(ctx, m.containerBin, "network", "create", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating network %s: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	log.Debug("created apple container network", "name", name)
	return name, nil
}

// RemoveNetwork removes an Apple container network by name.
// Best-effort: does not fail if network doesn't exist.
func (m *appleNetworkManager) RemoveNetwork(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, m.containerBin, "network", "delete", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		if strings.Contains(outStr, "not found") || strings.Contains(outStr, "No such") {
			return nil
		}
		return fmt.Errorf("removing network %s: %s: %w", name, outStr, err)
	}
	log.Debug("removed apple container network", "name", name)
	return nil
}

// ForceRemoveNetwork delegates to RemoveNetwork for Apple containers.
// Apple's container runtime handles disconnection automatically.
func (m *appleNetworkManager) ForceRemoveNetwork(ctx context.Context, name string) error {
	return m.RemoveNetwork(ctx, name)
}

// ListNetworks returns all moat-managed networks by filtering for moat- prefix.
// Apple container CLI has no label support, so we filter by naming convention.
func (m *appleNetworkManager) ListNetworks(ctx context.Context) ([]NetworkInfo, error) {
	cmd := exec.CommandContext(ctx, m.containerBin, "network", "list")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("listing networks: %s: %w", strings.TrimSpace(string(output)), err)
	}

	var result []NetworkInfo
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		name := strings.TrimSpace(line)
		if strings.HasPrefix(name, "moat-") {
			result = append(result, NetworkInfo{
				ID:   name,
				Name: name,
			})
		}
	}
	return result, nil
}
