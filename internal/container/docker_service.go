package container

import (
	"context"
	"fmt"
	"io"
	"sort"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// dockerServiceManager implements ServiceManager using Docker sidecars.
type dockerServiceManager struct {
	sidecar SidecarManager
	network NetworkManager
	cli     *client.Client
	// networkID is set externally when a network is created for the run.
	networkID string
}

// SetNetworkID sets the Docker network for service containers.
func (m *dockerServiceManager) SetNetworkID(id string) {
	m.networkID = id
}

// StartService provisions a service container.
func (m *dockerServiceManager) StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error) {
	if cfg.Image == "" {
		return ServiceInfo{}, fmt.Errorf("service %s: image is required", cfg.Name)
	}

	sidecarCfg := buildSidecarConfig(cfg, m.networkID)

	containerID, err := m.sidecar.StartSidecar(ctx, sidecarCfg)
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("starting %s service: %w", cfg.Name, err)
	}

	return buildServiceInfo(containerID, cfg, cfg.Name), nil
}

// CheckReady runs the service's readiness command inside the container.
func (m *dockerServiceManager) CheckReady(ctx context.Context, info ServiceInfo) error {
	if info.ReadinessCmd == "" {
		return nil
	}

	cmd := resolvePlaceholders(info.ReadinessCmd, info.Env, info.PasswordEnv)

	execCreateResp, err := m.cli.ContainerExecCreate(ctx, info.ID, dockercontainer.ExecOptions{
		Cmd:          []string{"sh", "-c", cmd},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("creating exec for readiness check: %w", err)
	}

	resp, err := m.cli.ContainerExecAttach(ctx, execCreateResp.ID, dockercontainer.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attaching to readiness check: %w", err)
	}
	// Drain output and wait for command to complete
	_, _ = io.Copy(io.Discard, resp.Reader)
	resp.Close()

	execInspect, err := m.cli.ContainerExecInspect(ctx, execCreateResp.ID)
	if err != nil {
		return fmt.Errorf("inspecting readiness check: %w", err)
	}
	if execInspect.ExitCode != 0 {
		return fmt.Errorf("readiness check failed with exit code %d", execInspect.ExitCode)
	}

	return nil
}

// StopService force-removes the service container.
func (m *dockerServiceManager) StopService(ctx context.Context, info ServiceInfo) error {
	return m.cli.ContainerRemove(ctx, info.ID, dockercontainer.RemoveOptions{Force: true})
}

// buildSidecarConfig converts a ServiceConfig into a SidecarConfig.
func buildSidecarConfig(cfg ServiceConfig, networkID string) SidecarConfig {
	image := cfg.Image + ":" + cfg.Version

	// Sort env var keys for deterministic ordering
	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

	envList := make([]string, 0, len(envKeys))
	for _, k := range envKeys {
		envList = append(envList, k+"="+cfg.Env[k])
	}

	sc := SidecarConfig{
		Image:     image,
		Name:      fmt.Sprintf("moat-%s-%s", cfg.Name, cfg.RunID),
		Hostname:  cfg.Name,
		NetworkID: networkID,
		RunID:     cfg.RunID,
		Env:       envList,
		Labels: map[string]string{
			"moat.role": "service",
		},
	}

	if len(cfg.ExtraCmd) > 0 {
		cmds := make([]string, len(cfg.ExtraCmd))
		for i, c := range cfg.ExtraCmd {
			cmds[i] = resolvePlaceholders(c, cfg.Env, cfg.PasswordEnv)
		}
		sc.Cmd = cmds
	}

	return sc
}
