// internal/trace/event_test.go
package trace

import (
	"testing"
	"time"
)

func TestExecEvent(t *testing.T) {
	event := ExecEvent{
		Timestamp:  time.Now(),
		PID:        1234,
		PPID:       1,
		Command:    "git",
		Args:       []string{"commit", "-m", "test"},
		WorkingDir: "/workspace",
	}

	if event.Command != "git" {
		t.Errorf("unexpected command: %s", event.Command)
	}
	if len(event.Args) != 3 {
		t.Errorf("unexpected args length: %d", len(event.Args))
	}
}

func TestExecEventIsGitCommit(t *testing.T) {
	tests := []struct {
		name  string
		event ExecEvent
		want  bool
	}{
		{
			name:  "git commit",
			event: ExecEvent{Command: "git", Args: []string{"commit", "-m", "msg"}},
			want:  true,
		},
		{
			name:  "git status",
			event: ExecEvent{Command: "git", Args: []string{"status"}},
			want:  false,
		},
		{
			name:  "not git",
			event: ExecEvent{Command: "ls", Args: []string{"-la"}},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.IsGitCommit(); got != tt.want {
				t.Errorf("IsGitCommit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecEventIsBuildCommand(t *testing.T) {
	tests := []struct {
		name  string
		event ExecEvent
		want  bool
	}{
		{
			name:  "npm run build",
			event: ExecEvent{Command: "npm", Args: []string{"run", "build"}},
			want:  true,
		},
		{
			name:  "go build",
			event: ExecEvent{Command: "go", Args: []string{"build", "./..."}},
			want:  true,
		},
		{
			name:  "make",
			event: ExecEvent{Command: "make", Args: []string{}},
			want:  true,
		},
		{
			name:  "cargo build",
			event: ExecEvent{Command: "cargo", Args: []string{"build"}},
			want:  true,
		},
		{
			name:  "not a build",
			event: ExecEvent{Command: "ls", Args: []string{"-la"}},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.IsBuildCommand(); got != tt.want {
				t.Errorf("IsBuildCommand() = %v, want %v", got, tt.want)
			}
		})
	}
}
