package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// containerIPMaxAttempts bounds how many times getContainerIP polls for a
// service container's address. Apple's container CLI assigns the IPv4 address
// asynchronously, so an inspect issued immediately after `run --detach` can
// return before the address is attached; polling lets the assignment land.
const containerIPMaxAttempts = 15

// containerIPRetryBase is the delay between address-lookup polls.
const containerIPRetryBase = 200 * time.Millisecond

// appleServiceManager implements ServiceManager using the Apple container CLI.
type appleServiceManager struct {
	containerBin string
	networkID    string

	// inspectFn runs `container inspect <id>` and returns raw output. nil means
	// use the real CLI; tests inject a fake.
	inspectFn func(ctx context.Context, id string) ([]byte, error)
	// ipRetryBase overrides containerIPRetryBase when non-zero (used by tests
	// to avoid real backoff sleeps).
	ipRetryBase time.Duration
}

// SetNetworkID sets the network for service containers.
func (m *appleServiceManager) SetNetworkID(id string) {
	m.networkID = id
}

// StartService starts a service container using the Apple container CLI.
func (m *appleServiceManager) StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error) {
	if cfg.Image == "" {
		return ServiceInfo{}, fmt.Errorf("service %s: image is required", cfg.Name)
	}

	// Pull image
	image := cfg.Image + ":" + cfg.Version
	pullCmd := exec.CommandContext(ctx, m.containerBin, "image", "pull", image)
	if pullOutput, pullErr := pullCmd.CombinedOutput(); pullErr != nil {
		return ServiceInfo{}, fmt.Errorf("pulling image %s: %s: %w", image, strings.TrimSpace(string(pullOutput)), pullErr)
	}

	args := buildAppleRunArgs(cfg, m.networkID)
	cmd := exec.CommandContext(ctx, m.containerBin, args...)
	// Read the container ID from stdout only. `container run --detach` writes
	// startup progress ("[1/6] Fetching image", ...) to stderr, so capturing
	// combined output would fold that progress into the container ID and break
	// the subsequent inspect. Stderr is captured separately for error messages.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("starting %s service: %s: %w", cfg.Name, strings.TrimSpace(stderr.String()), err)
	}

	containerID := parseRunContainerID(string(stdout))
	if containerID == "" {
		// `run --detach` reported success but printed no ID — fail fast with the
		// raw output instead of polling inspect on an empty ID for seconds.
		return ServiceInfo{}, fmt.Errorf("starting %s service: no container ID in run output: %q", cfg.Name, strings.TrimSpace(string(stdout)))
	}
	log.Debug("started apple service container", "service", cfg.Name, "container", containerID)

	// Apple containers don't support --hostname and DNS resolution by container
	// name requires system-level setup. Instead, inspect the container to get its
	// IP address and use that as the host for service connections.
	host, err := m.getContainerIP(ctx, containerID)
	if err != nil {
		// Clean up the container we just started
		_ = m.StopService(ctx, ServiceInfo{ID: containerID, Name: cfg.Name})
		return ServiceInfo{}, fmt.Errorf("getting IP for %s service: %w", cfg.Name, err)
	}
	log.Debug("resolved service container IP", "service", cfg.Name, "ip", host)

	return buildServiceInfo(containerID, cfg, host), nil
}

// CheckReady runs the readiness command inside the service container.
func (m *appleServiceManager) CheckReady(ctx context.Context, info ServiceInfo) error {
	if info.ReadinessCmd == "" {
		return nil
	}

	cmd := resolvePlaceholders(info.ReadinessCmd, info.Env, info.PasswordEnv)

	execCmd := exec.CommandContext(ctx, m.containerBin, "exec", info.ID, "sh", "-c", cmd)
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("readiness check failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return nil
}

// StopService force-removes the service container.
func (m *appleServiceManager) StopService(ctx context.Context, info ServiceInfo) error {
	cmd := exec.CommandContext(ctx, m.containerBin, "rm", "--force", info.ID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		if strings.Contains(outStr, "not found") || strings.Contains(outStr, "No such") {
			return nil
		}
		return fmt.Errorf("removing service container %s: %s: %w", info.Name, outStr, err)
	}
	return nil
}

// ProvisionService executes commands sequentially inside the service container.
func (m *appleServiceManager) ProvisionService(ctx context.Context, info ServiceInfo, cmds []string, stdout io.Writer) error {
	for _, cmd := range cmds {
		execCmd := exec.CommandContext(ctx, m.containerBin, "exec", info.ID, "sh", "-c", cmd)
		execCmd.Stdout = stdout
		execCmd.Stderr = stdout
		if err := execCmd.Run(); err != nil {
			return fmt.Errorf("provision command %q failed: %w", cmd, err)
		}
	}
	return nil
}

// runInspect executes `container inspect <id>`, using the injected inspectFn
// when present (tests) and the real CLI otherwise.
func (m *appleServiceManager) runInspect(ctx context.Context, containerID string) ([]byte, error) {
	if m.inspectFn != nil {
		return m.inspectFn(ctx, containerID)
	}
	cmd := exec.CommandContext(ctx, m.containerBin, "inspect", containerID)
	return cmd.CombinedOutput()
}

// parseContainerIPv4 extracts the IPv4 address (CIDR prefix stripped) from
// `container inspect` output. ok is false when the container has no address
// yet — the transient state right after `run --detach` that callers poll
// through. A non-nil error means the output could not be parsed at all.
func parseContainerIPv4(output []byte) (addr string, ok bool, err error) {
	info, err := parseAppleInspect(output)
	if err != nil {
		return "", false, fmt.Errorf("parsing inspect output: %w", err)
	}
	if len(info) == 0 {
		return "", false, nil
	}
	nets := info[0].networks()
	if len(nets) == 0 || nets[0].IPv4Address == "" {
		return "", false, nil
	}
	addr = nets[0].IPv4Address
	if idx := strings.IndexByte(addr, '/'); idx != -1 {
		addr = addr[:idx]
	}
	return addr, true, nil
}

// getContainerIP inspects a container and returns its IPv4 address on the
// network. Because Apple assigns the address asynchronously, it polls until
// the address appears (bounded by containerIPMaxAttempts) rather than failing
// on the first empty inspect.
func (m *appleServiceManager) getContainerIP(ctx context.Context, containerID string) (string, error) {
	base := m.ipRetryBase
	if base == 0 {
		base = containerIPRetryBase
	}

	var lastErr error
	for attempt := 0; attempt < containerIPMaxAttempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(base)
			select {
			case <-ctx.Done():
				timer.Stop()
				if lastErr != nil {
					return "", lastErr
				}
				return "", ctx.Err()
			case <-timer.C:
			}
		}

		output, err := m.runInspect(ctx, containerID)
		if err != nil {
			// Inspect can transiently fail right after start; retry.
			lastErr = fmt.Errorf("inspecting container: %s: %w", strings.TrimSpace(string(output)), err)
			continue
		}
		addr, ok, perr := parseContainerIPv4(output)
		if perr != nil {
			lastErr = perr
			continue
		}
		if ok {
			return addr, nil
		}
		lastErr = fmt.Errorf("no network address found for container %s", containerID)
	}
	return "", lastErr
}

// parseRunContainerID extracts the container ID from `container run --detach`
// stdout. The CLI may print stray lines before the ID, so the ID is taken as
// the last non-empty line. Returns "" when there is no usable output.
func parseRunContainerID(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// buildAppleContainerName returns the unique container name for a service.
func buildAppleContainerName(cfg ServiceConfig) string {
	return fmt.Sprintf("moat-%s-%s", cfg.Name, cfg.RunID)
}

// buildAppleRunArgs constructs CLI args for `container run`.
func buildAppleRunArgs(cfg ServiceConfig, networkID string) []string {
	image := cfg.Image + ":" + cfg.Version
	containerName := buildAppleContainerName(cfg)

	args := []string{
		"run", "--detach",
		"--name", containerName,
	}

	if networkID != "" {
		args = append(args, "--network", networkID)
	}

	if cfg.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dMB", cfg.MemoryMB))
	}

	// Add cache mount if configured
	if cfg.CachePath != "" && cfg.CacheHostPath != "" {
		args = append(args, "--volume", cfg.CacheHostPath+":"+cfg.CachePath)
	}

	// Sort env keys for deterministic ordering
	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

	for _, k := range envKeys {
		args = append(args, "--env", k+"="+cfg.Env[k])
	}

	args = append(args, image)

	if len(cfg.ExtraCmd) > 0 {
		for _, c := range cfg.ExtraCmd {
			args = append(args, resolvePlaceholders(c, cfg.Env, cfg.PasswordEnv))
		}
	}

	return args
}
