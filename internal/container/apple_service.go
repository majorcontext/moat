package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// appleServiceManager implements ServiceManager using the Apple container CLI.
type appleServiceManager struct {
	containerBin string
	networkID    string
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
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("starting %s service: %s: %w", cfg.Name, strings.TrimSpace(string(output)), err)
	}

	containerID := strings.TrimSpace(string(output))
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

// getContainerIP inspects a container and returns its IPv4 address on the network.
func (m *appleServiceManager) getContainerIP(ctx context.Context, containerID string) (string, error) {
	cmd := exec.CommandContext(ctx, m.containerBin, "inspect", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspecting container: %s: %w", strings.TrimSpace(string(output)), err)
	}

	var info []struct {
		Networks []struct {
			IPv4Address string `json:"ipv4Address"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		return "", fmt.Errorf("parsing inspect output: %w", err)
	}

	if len(info) == 0 || len(info[0].Networks) == 0 || info[0].Networks[0].IPv4Address == "" {
		return "", fmt.Errorf("no network address found for container %s", containerID)
	}

	// Address is in CIDR format (e.g., "192.168.68.2/24"), strip the prefix length
	addr := info[0].Networks[0].IPv4Address
	if idx := strings.IndexByte(addr, '/'); idx != -1 {
		addr = addr[:idx]
	}

	return addr, nil
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
