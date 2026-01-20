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
		// Positive cases - exact matches
		{
			name:  "npm run build",
			event: ExecEvent{Command: "npm", Args: []string{"run", "build"}},
			want:  true,
		},
		{
			name:  "npm run compile",
			event: ExecEvent{Command: "npm", Args: []string{"run", "compile"}},
			want:  true,
		},
		{
			name:  "go build",
			event: ExecEvent{Command: "go", Args: []string{"build"}},
			want:  true,
		},
		{
			name:  "go install",
			event: ExecEvent{Command: "go", Args: []string{"install"}},
			want:  true,
		},
		{
			name:  "make (bare)",
			event: ExecEvent{Command: "make", Args: []string{}},
			want:  true,
		},
		{
			name:  "make all",
			event: ExecEvent{Command: "make", Args: []string{"all"}},
			want:  true,
		},
		{
			name:  "make build",
			event: ExecEvent{Command: "make", Args: []string{"build"}},
			want:  true,
		},
		{
			name:  "cargo build",
			event: ExecEvent{Command: "cargo", Args: []string{"build"}},
			want:  true,
		},
		{
			name:  "yarn build",
			event: ExecEvent{Command: "yarn", Args: []string{"build"}},
			want:  true,
		},
		{
			name:  "mvn package",
			event: ExecEvent{Command: "mvn", Args: []string{"package"}},
			want:  true,
		},
		{
			name:  "mvn compile",
			event: ExecEvent{Command: "mvn", Args: []string{"compile"}},
			want:  true,
		},
		{
			name:  "gradle build",
			event: ExecEvent{Command: "gradle", Args: []string{"build"}},
			want:  true,
		},

		// Positive cases - with additional flags
		{
			name:  "go build with path",
			event: ExecEvent{Command: "go", Args: []string{"build", "./..."}},
			want:  true,
		},
		{
			name:  "npm run build with flags",
			event: ExecEvent{Command: "npm", Args: []string{"run", "build", "--", "--production"}},
			want:  true,
		},
		{
			name:  "cargo build with release",
			event: ExecEvent{Command: "cargo", Args: []string{"build", "--release"}},
			want:  true,
		},
		{
			name:  "yarn build with production",
			event: ExecEvent{Command: "yarn", Args: []string{"build", "--production"}},
			want:  true,
		},
		{
			name:  "make all with jobs",
			event: ExecEvent{Command: "make", Args: []string{"all", "-j4"}},
			want:  true,
		},

		// Negative cases - similar but not build commands
		{
			name:  "npm run build-docker (not a build)",
			event: ExecEvent{Command: "npm", Args: []string{"run", "build-docker"}},
			want:  false,
		},
		{
			name:  "npm run build-extra (not a build)",
			event: ExecEvent{Command: "npm", Args: []string{"run", "build-extra"}},
			want:  false,
		},
		{
			name:  "npm run builder (not a build)",
			event: ExecEvent{Command: "npm", Args: []string{"run", "builder"}},
			want:  false,
		},
		{
			name:  "npm install (not a build)",
			event: ExecEvent{Command: "npm", Args: []string{"install"}},
			want:  false,
		},
		{
			name:  "go test (not a build)",
			event: ExecEvent{Command: "go", Args: []string{"test", "./..."}},
			want:  false,
		},
		{
			name:  "go run (not a build)",
			event: ExecEvent{Command: "go", Args: []string{"run", "main.go"}},
			want:  false,
		},
		{
			name:  "make clean (not a build)",
			event: ExecEvent{Command: "make", Args: []string{"clean"}},
			want:  false,
		},
		{
			name:  "make test (not a build)",
			event: ExecEvent{Command: "make", Args: []string{"test"}},
			want:  false,
		},
		{
			name:  "cargo test (not a build)",
			event: ExecEvent{Command: "cargo", Args: []string{"test"}},
			want:  false,
		},
		{
			name:  "cargo run (not a build)",
			event: ExecEvent{Command: "cargo", Args: []string{"run"}},
			want:  false,
		},
		{
			name:  "yarn test (not a build)",
			event: ExecEvent{Command: "yarn", Args: []string{"test"}},
			want:  false,
		},
		{
			name:  "ls (unrelated command)",
			event: ExecEvent{Command: "ls", Args: []string{"-la"}},
			want:  false,
		},
		{
			name:  "git (unrelated command)",
			event: ExecEvent{Command: "git", Args: []string{"status"}},
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
