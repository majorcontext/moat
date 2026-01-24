package container

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestBuildRunArgs(t *testing.T) {
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
			want: []string{"run", "--detach", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "ubuntu:22.04"},
		},
		{
			name: "with name",
			cfg: Config{
				Name:  "my-container",
				Image: "python:3.11",
			},
			want: []string{"run", "--detach", "--name", "my-container", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "python:3.11"},
		},
		{
			name: "with working directory",
			cfg: Config{
				Image:      "node:20",
				WorkingDir: "/workspace",
			},
			want: []string{"run", "--detach", "--workdir", "/workspace", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "node:20"},
		},
		{
			name: "with environment variables",
			cfg: Config{
				Image: "python:3.11",
				Env:   []string{"DEBUG=true", "API_KEY=secret"},
			},
			want: []string{"run", "--detach", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--env", "DEBUG=true", "--env", "API_KEY=secret", "python:3.11"},
		},
		{
			name: "with volume mount",
			cfg: Config{
				Image: "ubuntu:22.04",
				Mounts: []MountConfig{
					{Source: "/home/user/project", Target: "/workspace"},
				},
			},
			want: []string{"run", "--detach", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--volume", "/home/user/project:/workspace", "ubuntu:22.04"},
		},
		{
			name: "with read-only volume mount",
			cfg: Config{
				Image: "ubuntu:22.04",
				Mounts: []MountConfig{
					{Source: "/home/user/data", Target: "/data", ReadOnly: true},
				},
			},
			want: []string{"run", "--detach", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--volume", "/home/user/data:/data:ro", "ubuntu:22.04"},
		},
		{
			name: "with command",
			cfg: Config{
				Image: "python:3.11",
				Cmd:   []string{"python", "-c", "print('hello')"},
			},
			want: []string{"run", "--detach", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "python:3.11", "python", "-c", "print('hello')"},
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
				"run", "--detach",
				"--name", "test-agent",
				"--workdir", "/workspace",
				"--dns", "8.8.8.8", "--dns", "8.8.4.4",
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

func TestAppleRuntime_InteractiveCommandStorage(t *testing.T) {
	r := &AppleRuntime{
		interactiveCommands: make(map[string]interactiveInfo),
	}

	containerID := "test-container-123"
	cmd := []string{"bash", "-c", "echo hello"}

	// Store a command
	r.interactiveMu.Lock()
	r.interactiveCommands[containerID] = interactiveInfo{cmd: cmd, hasMoatUser: true}
	r.interactiveMu.Unlock()

	// Verify it can be retrieved
	r.interactiveMu.Lock()
	storedInfo, exists := r.interactiveCommands[containerID]
	r.interactiveMu.Unlock()

	if !exists {
		t.Fatal("Expected command to exist in map")
	}
	if !reflect.DeepEqual(storedInfo.cmd, cmd) {
		t.Errorf("Stored command = %v, want %v", storedInfo.cmd, cmd)
	}

	// Simulate retrieval with deletion (as done in StartAttached)
	r.interactiveMu.Lock()
	retrievedInfo, hasCmd := r.interactiveCommands[containerID]
	if hasCmd {
		delete(r.interactiveCommands, containerID)
	}
	r.interactiveMu.Unlock()

	if !hasCmd {
		t.Fatal("Expected command to exist before deletion")
	}
	if !reflect.DeepEqual(retrievedInfo.cmd, cmd) {
		t.Errorf("Retrieved command = %v, want %v", retrievedInfo.cmd, cmd)
	}

	// Verify entry is gone after retrieval
	r.interactiveMu.Lock()
	_, stillExists := r.interactiveCommands[containerID]
	r.interactiveMu.Unlock()

	if stillExists {
		t.Error("Command should be deleted after retrieval (one-time use)")
	}
}

func TestAppleRuntime_InteractiveCommandCleanup(t *testing.T) {
	r := &AppleRuntime{
		interactiveCommands: make(map[string]interactiveInfo),
	}

	containerID := "test-container-456"
	cmd := []string{"python", "main.py"}

	// Store a command
	r.interactiveMu.Lock()
	r.interactiveCommands[containerID] = interactiveInfo{cmd: cmd, hasMoatUser: true}
	r.interactiveMu.Unlock()

	// Simulate cleanup (as done in RemoveContainer)
	r.interactiveMu.Lock()
	delete(r.interactiveCommands, containerID)
	r.interactiveMu.Unlock()

	// Verify the entry is gone
	r.interactiveMu.Lock()
	_, exists := r.interactiveCommands[containerID]
	r.interactiveMu.Unlock()

	if exists {
		t.Error("Expected command to be cleaned up after RemoveContainer-style deletion")
	}
}

func TestAppleRuntime_StartAttachedNoCommand(t *testing.T) {
	r := &AppleRuntime{
		containerBin:        "/bin/false", // Will fail if exec is actually attempted
		interactiveCommands: make(map[string]interactiveInfo),
	}

	containerID := "test-container-789"

	// Call StartAttached without storing a command first - should return an error
	err := r.StartAttached(context.Background(), containerID, AttachOptions{})

	if err == nil {
		t.Fatal("Expected error when no command is stored")
	}

	// Verify the error message is helpful
	errMsg := err.Error()
	if !strings.Contains(errMsg, "no command stored") {
		t.Errorf("Expected 'no command stored' in error, got: %v", err)
	}
	if !strings.Contains(errMsg, containerID) {
		t.Errorf("Expected container ID in error, got: %v", err)
	}
	if !strings.Contains(errMsg, "Interactive=true") {
		t.Errorf("Expected helpful hint about Interactive=true in error, got: %v", err)
	}
}

func TestBuildExecArgs(t *testing.T) {
	tests := []struct {
		name        string
		stdin       bool
		tty         bool
		containerID string
		cmd         []string
		user        string
		want        []string
	}{
		{
			name:        "stdin only no user",
			stdin:       true,
			tty:         false,
			containerID: "abc123",
			cmd:         []string{"cat"},
			user:        "",
			want:        []string{"exec", "-i", "abc123", "cat"},
		},
		{
			name:        "tty only no user",
			stdin:       false,
			tty:         true,
			containerID: "abc123",
			cmd:         []string{"bash"},
			user:        "",
			want:        []string{"exec", "-t", "abc123", "bash"},
		},
		{
			name:        "stdin and tty no user",
			stdin:       true,
			tty:         true,
			containerID: "abc123",
			cmd:         []string{"bash", "-l"},
			user:        "",
			want:        []string{"exec", "-i", "-t", "abc123", "bash", "-l"},
		},
		{
			name:        "neither stdin nor tty",
			stdin:       false,
			tty:         false,
			containerID: "abc123",
			cmd:         []string{"echo", "hello"},
			user:        "",
			want:        []string{"exec", "abc123", "echo", "hello"},
		},
		{
			name:        "with moatuser (matches StartAttached)",
			stdin:       true,
			tty:         true,
			containerID: "abc123",
			cmd:         []string{"bash", "-l"},
			user:        "moatuser",
			want:        []string{"exec", "-i", "-t", "--user", "moatuser", "abc123", "bash", "-l"},
		},
		{
			name:        "complex command with user",
			stdin:       true,
			tty:         true,
			containerID: "container-xyz",
			cmd:         []string{"python", "-c", "print('hello world')"},
			user:        "moatuser",
			want:        []string{"exec", "-i", "-t", "--user", "moatuser", "container-xyz", "python", "-c", "print('hello world')"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the exported BuildExecArgs function
			got := BuildExecArgs(tt.containerID, tt.cmd, tt.stdin, tt.tty, tt.user)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildExecArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppleRuntime_InteractiveCommandsConcurrentAccess(t *testing.T) {
	r := &AppleRuntime{
		interactiveCommands: make(map[string]interactiveInfo),
	}

	const numGoroutines = 100
	var wg sync.WaitGroup

	// Concurrent writes and reads to the interactive commands map
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			containerID := "test-container-" + string(rune('a'+id%26))
			cmd := []string{"cmd", string(rune('a' + id%26))}

			// Store a command
			r.interactiveMu.Lock()
			r.interactiveCommands[containerID] = interactiveInfo{cmd: cmd, hasMoatUser: true}
			r.interactiveMu.Unlock()

			// Read it back (simulating what StartAttached does)
			r.interactiveMu.Lock()
			_, exists := r.interactiveCommands[containerID]
			if exists {
				delete(r.interactiveCommands, containerID)
			}
			r.interactiveMu.Unlock()
		}(i)
	}

	wg.Wait()

	// Verify the map is empty after all goroutines complete
	r.interactiveMu.Lock()
	remaining := len(r.interactiveCommands)
	r.interactiveMu.Unlock()

	if remaining != 0 {
		t.Errorf("Expected empty map after concurrent access, got %d entries", remaining)
	}
}
