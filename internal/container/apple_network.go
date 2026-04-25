package container

import (
	"context"
	"encoding/json"
	"errors"
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
	callCtx, cancel := context.WithTimeout(ctx, networkCreateTimeout)
	defer cancel()

	cmd := exec.CommandContext(callCtx, m.containerBin, "network", "create", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If the bounded timeout fired (and the caller's ctx is still alive),
		// surface a hint about the most likely cause: leaked networks from
		// prior runs exhausting the IP pool.
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return "", fmt.Errorf("creating network %s: timed out after %s — Apple's container IP pool may be exhausted by orphaned moat networks. List with `container network list` and remove unused entries with `container network delete <name>`",
				name, networkCreateTimeout)
		}
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
	return parseAppleNetworkList(string(output)), nil
}

// parseAppleNetworkList extracts moat-managed network names from the output of
// `container network list`. The output is multi-column with a header row:
//
//	NETWORK                STATE    SUBNET
//	moat-run_abc123def456  running  192.168.65.0/24
//	default                running  192.168.64.0/24
//
// Only the first whitespace-separated token on each line is the network name;
// rows that don't begin with "moat-" are ignored (including the header).
func parseAppleNetworkList(output string) []NetworkInfo {
	var result []NetworkInfo
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if strings.HasPrefix(name, "moat-") {
			result = append(result, NetworkInfo{
				ID:   name,
				Name: name,
			})
		}
	}
	return result
}

// NetworkGateway returns the IPv4 gateway for the named Apple container network.
func (m *appleNetworkManager) NetworkGateway(ctx context.Context, networkID string) string {
	return inspectAppleNetworkGateway(ctx, m.containerBin, networkID)
}

// inspectAppleNetworkGateway runs `container network inspect` and extracts the
// IPv4 gateway address from the JSON output. Returns empty string on failure.
// Used by both probeDefaultGateway (init-time default network) and
// NetworkGateway (per-run custom networks).
func inspectAppleNetworkGateway(ctx context.Context, containerBin, networkName string) string {
	cmd := exec.CommandContext(ctx, containerBin, "network", "inspect", networkName)
	out, err := cmd.Output()
	if err != nil {
		log.Debug("failed to inspect network for gateway", "network", networkName, "error", err)
		return ""
	}

	var networks []struct {
		Status struct {
			IPv4Gateway string `json:"ipv4Gateway"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &networks); err != nil || len(networks) == 0 {
		log.Debug("failed to parse network inspect for gateway", "network", networkName, "error", err)
		return ""
	}

	return networks[0].Status.IPv4Gateway
}
