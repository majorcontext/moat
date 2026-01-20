// Package trace provides execution event types for capturing commands run in containers.
package trace

import (
	"strings"
	"time"
)

// ExecEvent represents a command execution captured by the tracer.
type ExecEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	PID        int            `json:"pid"`
	PPID       int            `json:"ppid"`
	Command    string         `json:"command"`
	Args       []string       `json:"args"`
	WorkingDir string         `json:"working_dir,omitempty"`
	ExitCode   *int           `json:"exit_code,omitempty"`
	Duration   *time.Duration `json:"duration,omitempty"`
}

// IsGitCommit returns true if this event is a git commit command.
func (e ExecEvent) IsGitCommit() bool {
	if e.Command != "git" {
		return false
	}
	for _, arg := range e.Args {
		if arg == "commit" {
			return true
		}
	}
	return false
}

// IsBuildCommand returns true if this event is a build command.
func (e ExecEvent) IsBuildCommand() bool {
	// Check for common build commands
	buildCommands := map[string][]string{
		"npm":    {"run build", "run compile"},
		"yarn":   {"build"},
		"go":     {"build", "install"},
		"make":   {"", "all", "build"},
		"cargo":  {"build"},
		"mvn":    {"package", "compile"},
		"gradle": {"build"},
	}

	patterns, ok := buildCommands[e.Command]
	if !ok {
		return false
	}

	argsStr := strings.Join(e.Args, " ")
	for _, pattern := range patterns {
		if pattern == "" && len(e.Args) == 0 {
			return true // bare "make"
		}
		if strings.HasPrefix(argsStr, pattern) {
			return true
		}
	}
	return false
}
