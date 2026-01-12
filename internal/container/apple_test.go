package container

import (
	"reflect"
	"testing"
)

func TestBuildRunArgs(t *testing.T) {
	tests := []struct {
		name string
		cfg  ContainerConfig
		want []string
	}{
		{
			name: "basic image only",
			cfg: ContainerConfig{
				Image: "ubuntu:22.04",
			},
			want: []string{"run", "--detach", "ubuntu:22.04"},
		},
		{
			name: "with name",
			cfg: ContainerConfig{
				Name:  "my-container",
				Image: "python:3.11",
			},
			want: []string{"run", "--detach", "--name", "my-container", "python:3.11"},
		},
		{
			name: "with working directory",
			cfg: ContainerConfig{
				Image:      "node:20",
				WorkingDir: "/workspace",
			},
			want: []string{"run", "--detach", "--workdir", "/workspace", "node:20"},
		},
		{
			name: "with environment variables",
			cfg: ContainerConfig{
				Image: "python:3.11",
				Env:   []string{"DEBUG=true", "API_KEY=secret"},
			},
			want: []string{"run", "--detach", "--env", "DEBUG=true", "--env", "API_KEY=secret", "python:3.11"},
		},
		{
			name: "with volume mount",
			cfg: ContainerConfig{
				Image: "ubuntu:22.04",
				Mounts: []MountConfig{
					{Source: "/home/user/project", Target: "/workspace"},
				},
			},
			want: []string{"run", "--detach", "--volume", "/home/user/project:/workspace", "ubuntu:22.04"},
		},
		{
			name: "with read-only volume mount",
			cfg: ContainerConfig{
				Image: "ubuntu:22.04",
				Mounts: []MountConfig{
					{Source: "/home/user/data", Target: "/data", ReadOnly: true},
				},
			},
			want: []string{"run", "--detach", "--volume", "/home/user/data:/data:ro", "ubuntu:22.04"},
		},
		{
			name: "with command",
			cfg: ContainerConfig{
				Image: "python:3.11",
				Cmd:   []string{"python", "-c", "print('hello')"},
			},
			want: []string{"run", "--detach", "python:3.11", "python", "-c", "print('hello')"},
		},
		{
			name: "full config",
			cfg: ContainerConfig{
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
				"run", "--detach",
				"--name", "test-agent",
				"--workdir", "/workspace",
				"--env", "DEBUG=true",
				"--volume", "/home/user/project:/workspace",
				"--volume", "/home/user/cache:/cache:ro",
				"python:3.11",
				"python", "main.py",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildRunArgs(tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildRunArgs() = %v, want %v", got, tt.want)
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
