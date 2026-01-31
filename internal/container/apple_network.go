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
