package config

import "fmt"

// WorkspaceMode selects how the host working tree is presented to the container.
type WorkspaceMode string

const (
	// WorkspaceModeBind bind-mounts the host tree at /workspace (default, current behavior).
	WorkspaceModeBind WorkspaceMode = "bind"
	// WorkspaceModeVolume copies the host tree into an ephemeral Docker named volume.
	WorkspaceModeVolume WorkspaceMode = "volume"
)

// WorkspaceConfig is the moat.yaml `workspace:` block.
type WorkspaceConfig struct {
	// Mode is "bind" (default) or "volume". Empty means bind.
	Mode WorkspaceMode `yaml:"mode,omitempty"`
}

// Validate rejects any mode other than "", "bind", or "volume".
func (w WorkspaceConfig) Validate() error {
	switch w.Mode {
	case "", WorkspaceModeBind, WorkspaceModeVolume:
		return nil
	default:
		return fmt.Errorf("workspace.mode %q is invalid (must be 'bind' or 'volume')", w.Mode)
	}
}

// ResolveWorkspaceMode applies precedence: CLI override > yaml > default(bind).
// override is the raw --workspace-mode flag value ("" when unset). It also
// validates w.Mode, so it is safe to call without a prior Load().
func ResolveWorkspaceMode(w WorkspaceConfig, override string) (WorkspaceMode, error) {
	pick := w.Mode
	if override != "" {
		pick = WorkspaceMode(override)
	}
	switch pick {
	case "", WorkspaceModeBind:
		return WorkspaceModeBind, nil
	case WorkspaceModeVolume:
		return WorkspaceModeVolume, nil
	default:
		return "", fmt.Errorf("workspace mode %q is invalid (must be 'bind' or 'volume')", pick)
	}
}
