package container

import (
	"context"
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

	return buildServiceInfo(containerID, cfg), nil
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

// buildAppleRunArgs constructs CLI args for `container run`.
func buildAppleRunArgs(cfg ServiceConfig, networkID string) []string {
	image := cfg.Image + ":" + cfg.Version
	containerName := fmt.Sprintf("moat-%s-%s", cfg.Name, cfg.RunID)

	args := []string{
		"run", "--detach",
		"--name", containerName,
		"--hostname", cfg.Name,
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
