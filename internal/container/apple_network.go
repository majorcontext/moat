package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// networkDeleteMaxAttempts bounds how many times RemoveNetwork retries when
// Apple's runtime reports the network is still in use. Apple's `container`
// CLI tears containers down asynchronously, so `container rm` can return
// before the container has detached from its network; an immediate
// `network delete` then fails with "active containers" / "pending operation".
// Retrying with backoff lets the async detach complete.
const networkDeleteMaxAttempts = 5

// networkDeleteRetryBase is the base delay between delete retries; each
// attempt waits networkDeleteRetryBase * 2^(attempt-1).
const networkDeleteRetryBase = 500 * time.Millisecond

// appleNetworkManager implements NetworkManager using the Apple container CLI.
type appleNetworkManager struct {
	containerBin string

	// deleteFn runs `container network delete <name>` and returns the trimmed
	// combined output. nil means use the real CLI; tests inject a fake.
	deleteFn func(ctx context.Context, name string) (string, error)
	// retryBase overrides networkDeleteRetryBase when non-zero (used by tests
	// to avoid real backoff sleeps).
	retryBase time.Duration
}

// runDelete executes the `network delete` command, using the injected deleteFn
// when present (tests) and the real CLI otherwise.
func (m *appleNetworkManager) runDelete(ctx context.Context, name string) (string, error) {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, name)
	}
	cmd := exec.CommandContext(ctx, m.containerBin, "network", "delete", name)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// isRetryableAppleNetworkDeleteError reports whether a failed `network delete`
// output indicates a transient condition that should resolve once Apple's
// asynchronous container teardown completes. Matched case-insensitively
// against strings observed from the Apple container CLI.
func isRetryableAppleNetworkDeleteError(output string) bool {
	s := strings.ToLower(output)
	for _, marker := range []string{
		"active container",                // "...cannot be disabled with active containers"
		"ip allocator cannot be disabled", // same error, alternate phrasing
		"pending operation",               // "network ... has a pending operation"
		"in use",                          // generic "network is in use"
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
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
// Best-effort: does not fail if the network doesn't exist.
//
// Apple removes containers asynchronously, so a delete issued right after the
// run's containers are removed can fail because they haven't detached yet.
// Such failures are retried with exponential backoff until the detach
// completes (or networkDeleteMaxAttempts is reached); non-transient failures
// return immediately.
func (m *appleNetworkManager) RemoveNetwork(ctx context.Context, name string) error {
	base := m.retryBase
	if base == 0 {
		base = networkDeleteRetryBase
	}

	var lastErr error
	for attempt := 0; attempt < networkDeleteMaxAttempts; attempt++ {
		if attempt > 0 {
			delay := base * (1 << (attempt - 1))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				if lastErr != nil {
					return lastErr
				}
				return ctx.Err()
			case <-timer.C:
			}
		}

		output, err := m.runDelete(ctx, name)
		if err == nil {
			log.Debug("removed apple container network", "name", name)
			return nil
		}
		if strings.Contains(output, "not found") || strings.Contains(output, "No such") {
			return nil
		}

		lastErr = fmt.Errorf("removing network %s: %s: %w", name, output, err)
		if !isRetryableAppleNetworkDeleteError(output) {
			return lastErr
		}
		log.Debug("apple network delete failed, retrying after async detach",
			"name", name, "attempt", attempt+1, "error", output)
	}
	return lastErr
}

// ForceRemoveNetwork delegates to RemoveNetwork, which already retries through
// Apple's asynchronous container detach. Apple's container runtime has no
// separate force-disconnect step.
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
