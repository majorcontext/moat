package container

import (
	"context"
	"fmt"
	"strings"

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

	return buildServiceInfo(containerID, cfg), nil
}

// CheckReady runs the service's readiness command inside the container.
func (m *dockerServiceManager) CheckReady(ctx context.Context, info ServiceInfo) error {
	if info.ReadinessCmd == "" {
		return nil
	}

	cmd := resolveReadinessCmd(info.ReadinessCmd, info.Env, info.PasswordEnv)

	execCreateResp, err := m.cli.ContainerExecCreate(ctx, info.ID, dockercontainer.ExecOptions{
		Cmd: []string{"sh", "-c", cmd},
	})
	if err != nil {
		return fmt.Errorf("creating exec for readiness check: %w", err)
	}

	if err := m.cli.ContainerExecStart(ctx, execCreateResp.ID, dockercontainer.ExecStartOptions{}); err != nil {
		return fmt.Errorf("running readiness check: %w", err)
	}

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

	var envList []string
	for k, v := range cfg.Env {
		envList = append(envList, k+"="+v)
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
			cmds[i] = c
			for k, v := range cfg.Env {
				cmds[i] = strings.ReplaceAll(cmds[i], "{"+strings.ToLower(k)+"}", v)
			}
		}
		sc.Cmd = cmds
	}

	return sc
}

// buildServiceInfo creates a ServiceInfo from a started container.
func buildServiceInfo(containerID string, cfg ServiceConfig) ServiceInfo {
	return ServiceInfo{
		ID:           containerID,
		Name:         cfg.Name,
		Host:         cfg.Name,
		Ports:        cfg.Ports,
		Env:          cfg.Env,
		ReadinessCmd: cfg.ReadinessCmd,
		PasswordEnv:  cfg.PasswordEnv,
	}
}

// resolveReadinessCmd substitutes password placeholders in readiness commands.
func resolveReadinessCmd(cmd string, env map[string]string, passwordEnv string) string {
	if passwordEnv != "" {
		if pw, ok := env[passwordEnv]; ok {
			cmd = strings.ReplaceAll(cmd, "{password}", pw)
		}
	}
	if pw, ok := env["password"]; ok {
		cmd = strings.ReplaceAll(cmd, "{password}", pw)
	}
	return cmd
}
