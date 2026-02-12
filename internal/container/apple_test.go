package container

import (
	"os"
	"reflect"
	"testing"

	"github.com/creack/pty"
)

func TestBuildCreateArgs(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want []string
	}{
		{
			name: "basic image only",
			cfg: Config{
				Image: "ubuntu:22.04",
			},
			want: []string{"create", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "ubuntu:22.04"},
		},
		{
			name: "with name",
			cfg: Config{
				Name:  "my-container",
				Image: "python:3.11",
			},
			want: []string{"create", "--name", "my-container", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "python:3.11"},
		},
		{
			name: "with working directory",
			cfg: Config{
				Image:      "node:20",
				WorkingDir: "/workspace",
			},
			want: []string{"create", "--memory", "4096MB", "--workdir", "/workspace", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "node:20"},
		},
		{
			name: "with environment variables",
			cfg: Config{
				Image: "python:3.11",
				Env:   []string{"DEBUG=true", "API_KEY=secret"},
			},
			want: []string{"create", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--env", "DEBUG=true", "--env", "API_KEY=secret", "python:3.11"},
		},
		{
			name: "with volume mount",
			cfg: Config{
				Image: "ubuntu:22.04",
				Mounts: []MountConfig{
					{Source: "/home/user/project", Target: "/workspace"},
				},
			},
			want: []string{"create", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--volume", "/home/user/project:/workspace", "ubuntu:22.04"},
		},
		{
			name: "with read-only volume mount",
			cfg: Config{
				Image: "ubuntu:22.04",
				Mounts: []MountConfig{
					{Source: "/home/user/data", Target: "/data", ReadOnly: true},
				},
			},
			want: []string{"create", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--volume", "/home/user/data:/data:ro", "ubuntu:22.04"},
		},
		{
			name: "with command",
			cfg: Config{
				Image: "python:3.11",
				Cmd:   []string{"python", "-c", "print('hello')"},
			},
			want: []string{"create", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "python:3.11", "python", "-c", "print('hello')"},
		},
		{
			name: "full config",
			cfg: Config{
				Name:       "test-agent",
				Image:      "python:3.11",
				WorkingDir: "/workspace",
				Env:        []string{"DEBUG=true"},
				Mounts: []MountConfig{
					{Source: "/home/user/project", Target: "/workspace"},
					{Source: "/home/user/cache", Target: "/cache", ReadOnly: true},
				},
				Cmd: []string{"python", "main.py"},
			},
			want: []string{
				"create",
				"--name", "test-agent",
				"--memory", "4096MB",
				"--workdir", "/workspace",
				"--dns", "8.8.8.8", "--dns", "8.8.4.4",
				"--env", "DEBUG=true",
				"--volume", "/home/user/project:/workspace",
				"--volume", "/home/user/cache:/cache:ro",
				"python:3.11",
				"python", "main.py",
			},
		},
		{
			name: "interactive mode",
			cfg: Config{
				Image:       "ubuntu:22.04",
				Interactive: true,
			},
			// Note: -t flag is only added when os.Stdin is a real terminal,
			// which it's not during tests, so we only expect -i here.
			want: []string{"create", "-i", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "ubuntu:22.04"},
		},
		{
			name: "custom memory",
			cfg: Config{
				Image:    "ubuntu:22.04",
				MemoryMB: 8192,
			},
			want: []string{"create", "--memory", "8192MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "ubuntu:22.04"},
		},
		{
			name: "custom cpus",
			cfg: Config{
				Image: "ubuntu:22.04",
				CPUs:  8,
			},
			want: []string{"create", "--memory", "4096MB", "--cpus", "8", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "ubuntu:22.04"},
		},
		{
			name: "custom memory and cpus",
			cfg: Config{
				Image:    "ubuntu:22.04",
				MemoryMB: 16384,
				CPUs:     12,
			},
			want: []string{"create", "--memory", "16384MB", "--cpus", "12", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "ubuntu:22.04"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildCreateArgs(tt.cfg)
			if err != nil {
				t.Fatalf("BuildCreateArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildCreateArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppleRuntime_GetHostAddress(t *testing.T) {
	r := &AppleRuntime{
		hostAddress: "192.168.64.1",
	}

	got := r.GetHostAddress()
	want := "192.168.64.1"

	if got != want {
		t.Errorf("GetHostAddress() = %v, want %v", got, want)
	}
}

func TestAppleRuntime_Type(t *testing.T) {
	r := &AppleRuntime{}

	got := r.Type()
	want := RuntimeApple

	if got != want {
		t.Errorf("Type() = %v, want %v", got, want)
	}
}

func TestAppleRuntime_SupportsHostNetwork(t *testing.T) {
	r := &AppleRuntime{}

	if r.SupportsHostNetwork() {
		t.Error("SupportsHostNetwork() = true, want false")
	}
}

func TestAppleRuntime_ResizeTTY_NoActivePTY(t *testing.T) {
	r := &AppleRuntime{}

	// ResizeTTY should return nil when no PTY is tracked
	err := r.ResizeTTY(t.Context(), "nonexistent-container", 24, 80)
	if err != nil {
		t.Errorf("ResizeTTY() with no active PTY = %v, want nil", err)
	}
}

func TestAppleRuntime_ResizeTTY_WithActivePTY(t *testing.T) {
	// Create a real PTY pair to test resizing
	ptmx, pts, err := pty.Open()
	if err != nil {
		t.Fatalf("failed to open pty: %v", err)
	}
	defer ptmx.Close()
	defer pts.Close()

	r := &AppleRuntime{
		activePTY: map[string]*os.File{
			"test-container": ptmx,
		},
	}

	// ResizeTTY should succeed and actually resize the PTY
	err = r.ResizeTTY(t.Context(), "test-container", 50, 120)
	if err != nil {
		t.Fatalf("ResizeTTY() = %v, want nil", err)
	}

	// Verify the PTY was actually resized by reading the size back
	size, err := pty.GetsizeFull(ptmx)
	if err != nil {
		t.Fatalf("GetsizeFull() = %v", err)
	}
	if size.Rows != 50 {
		t.Errorf("PTY rows = %d, want 50", size.Rows)
	}
	if size.Cols != 120 {
		t.Errorf("PTY cols = %d, want 120", size.Cols)
	}
}
