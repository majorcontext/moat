package run

import (
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/deps"
)

// TestHasDockerDependency verifies the detection of docker dependency in a list.
func TestHasDockerDependency(t *testing.T) {
	tests := []struct {
		name    string
		depList []deps.Dependency
		want    bool
	}{
		{
			name:    "empty list",
			depList: nil,
			want:    false,
		},
		{
			name: "no docker",
			depList: []deps.Dependency{
				{Name: "node", Version: "20"},
				{Name: "go", Version: "1.22"},
			},
			want: false,
		},
		{
			name: "docker present with mode",
			depList: []deps.Dependency{
				{Name: "node", Version: "20"},
				{Name: "docker", DockerMode: deps.DockerModeHost},
			},
			want: true,
		},
		{
			name: "docker only with mode",
			depList: []deps.Dependency{
				{Name: "docker", DockerMode: deps.DockerModeHost},
			},
			want: true,
		},
		{
			name: "docker host mode",
			depList: []deps.Dependency{
				{Name: "docker", DockerMode: deps.DockerModeHost},
			},
			want: true,
		},
		{
			name: "docker dind mode",
			depList: []deps.Dependency{
				{Name: "docker", DockerMode: deps.DockerModeDind},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasDockerDependency(tt.depList)
			if got != tt.want {
				t.Errorf("HasDockerDependency() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetDockerDependency verifies retrieval of docker dependency with mode.
func TestGetDockerDependency(t *testing.T) {
	tests := []struct {
		name     string
		depList  []deps.Dependency
		wantNil  bool
		wantMode deps.DockerMode
	}{
		{
			name:    "empty list returns nil",
			depList: nil,
			wantNil: true,
		},
		{
			name: "no docker returns nil",
			depList: []deps.Dependency{
				{Name: "node", Version: "20"},
			},
			wantNil: true,
		},
		{
			name: "docker host mode",
			depList: []deps.Dependency{
				{Name: "docker", DockerMode: deps.DockerModeHost},
			},
			wantNil:  false,
			wantMode: deps.DockerModeHost,
		},
		{
			name: "docker dind mode",
			depList: []deps.Dependency{
				{Name: "docker", DockerMode: deps.DockerModeDind},
			},
			wantNil:  false,
			wantMode: deps.DockerModeDind,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetDockerDependency(tt.depList)
			if tt.wantNil {
				if got != nil {
					t.Errorf("GetDockerDependency() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("GetDockerDependency() = nil, want non-nil")
			}
			if got.DockerMode != tt.wantMode {
				t.Errorf("GetDockerDependency().DockerMode = %q, want %q", got.DockerMode, tt.wantMode)
			}
		})
	}
}

// TestValidateDockerDependency verifies validation rules for docker modes and runtimes.
func TestValidateDockerDependency(t *testing.T) {
	tests := []struct {
		name        string
		runtimeType container.RuntimeType
		mode        deps.DockerMode
		wantErr     bool
		errType     string // "host" or "dind" for expected error type
	}{
		{
			name:        "host mode on Docker runtime allowed",
			runtimeType: container.RuntimeDocker,
			mode:        deps.DockerModeHost,
			wantErr:     false,
		},
		{
			name:        "host mode on Apple runtime rejected",
			runtimeType: container.RuntimeApple,
			mode:        deps.DockerModeHost,
			wantErr:     true,
			errType:     "host",
		},
		{
			name:        "dind mode on Docker runtime allowed",
			runtimeType: container.RuntimeDocker,
			mode:        deps.DockerModeDind,
			wantErr:     false,
		},
		{
			name:        "dind mode on Apple runtime rejected",
			runtimeType: container.RuntimeApple,
			mode:        deps.DockerModeDind,
			wantErr:     true,
			errType:     "dind",
		},
		{
			name:        "empty mode (defaults to host) on Docker runtime allowed",
			runtimeType: container.RuntimeDocker,
			mode:        "",
			wantErr:     false,
		},
		{
			name:        "empty mode (defaults to host) on Apple runtime rejected",
			runtimeType: container.RuntimeApple,
			mode:        "",
			wantErr:     true,
			errType:     "host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDockerDependency(tt.runtimeType, tt.mode)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDockerDependency() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				switch tt.errType {
				case "host":
					if _, ok := err.(ErrDockerHostRequiresDockerRuntime); !ok {
						t.Errorf("error type = %T, want ErrDockerHostRequiresDockerRuntime", err)
					}
				case "dind":
					if _, ok := err.(ErrDockerDindRequiresDockerRuntime); !ok {
						t.Errorf("error type = %T, want ErrDockerDindRequiresDockerRuntime", err)
					}
				}
			}
		})
	}
}

// TestErrDockerHostRequiresDockerRuntimeMessage verifies the error message
// includes actionable guidance for users.
func TestErrDockerHostRequiresDockerRuntimeMessage(t *testing.T) {
	err := ErrDockerHostRequiresDockerRuntime{}
	msg := err.Error()

	// Check for key parts of the error message
	mustContain := []string{
		"'docker:host' dependency requires Docker runtime",
		"Apple containers cannot access",
		"docker:dind",
		"moat run --runtime docker",
	}

	for _, s := range mustContain {
		if !containsString(msg, s) {
			t.Errorf("error message missing %q:\n%s", s, msg)
		}
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStringHelper(s, substr))
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestResolveDockerDependency_NoDependency verifies nil is returned
// when docker is not in the dependency list.
func TestResolveDockerDependency_NoDependency(t *testing.T) {
	depList := []deps.Dependency{
		{Name: "node", Version: "20"},
	}

	cfg, err := ResolveDockerDependency(depList, container.RuntimeDocker)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config when docker not present, got %+v", cfg)
	}
}

// TestResolveDockerDependency_AppleRuntime_HostMode verifies Apple containers
// return an error when docker:host dependency is present.
func TestResolveDockerDependency_AppleRuntime_HostMode(t *testing.T) {
	depList := []deps.Dependency{
		{Name: "docker", DockerMode: deps.DockerModeHost},
	}

	cfg, err := ResolveDockerDependency(depList, container.RuntimeApple)
	if err == nil {
		t.Fatal("expected error for Apple runtime with docker:host dependency")
	}
	if cfg != nil {
		t.Errorf("expected nil config on error, got %+v", cfg)
	}

	// Verify it's the right error type
	if _, ok := err.(ErrDockerHostRequiresDockerRuntime); !ok {
		t.Errorf("expected ErrDockerHostRequiresDockerRuntime, got %T: %v", err, err)
	}
}

// TestResolveDockerDependency_AppleRuntime_DindMode verifies Apple containers
// reject docker:dind mode (privileged mode not supported).
func TestResolveDockerDependency_AppleRuntime_DindMode(t *testing.T) {
	depList := []deps.Dependency{
		{Name: "docker", DockerMode: deps.DockerModeDind},
	}

	cfg, err := ResolveDockerDependency(depList, container.RuntimeApple)
	if err == nil {
		t.Fatal("expected error for Apple runtime with docker:dind dependency")
	}
	if cfg != nil {
		t.Errorf("expected nil config on error, got %+v", cfg)
	}

	// Verify it's the right error type
	if _, ok := err.(ErrDockerDindRequiresDockerRuntime); !ok {
		t.Errorf("expected ErrDockerDindRequiresDockerRuntime, got %T: %v", err, err)
	}
}

// TestResolveDockerDependency_DockerRuntime_HostMode verifies the socket mount
// and GID are returned when docker:host dependency is present with Docker runtime.
func TestResolveDockerDependency_DockerRuntime_HostMode(t *testing.T) {
	// Skip if docker socket doesn't exist (e.g., in CI without Docker)
	if _, err := os.Stat(DockerSocketPath); os.IsNotExist(err) {
		t.Skipf("docker socket not available at %s", DockerSocketPath)
	}

	depList := []deps.Dependency{
		{Name: "node", Version: "20"},
		{Name: "docker", DockerMode: deps.DockerModeHost},
	}

	cfg, err := ResolveDockerDependency(depList, container.RuntimeDocker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	// Verify mode
	if cfg.Mode != deps.DockerModeHost {
		t.Errorf("Mode = %q, want %q", cfg.Mode, deps.DockerModeHost)
	}

	// Verify socket mount
	if cfg.SocketMount.Source != DockerSocketPath {
		t.Errorf("SocketMount.Source = %q, want %q", cfg.SocketMount.Source, DockerSocketPath)
	}
	if cfg.SocketMount.Target != DockerSocketPath {
		t.Errorf("SocketMount.Target = %q, want %q", cfg.SocketMount.Target, DockerSocketPath)
	}
	if cfg.SocketMount.ReadOnly {
		t.Error("SocketMount.ReadOnly should be false")
	}

	// Verify GID is a valid number
	if cfg.GroupID == "" {
		t.Error("GroupID should not be empty")
	}
	// The GID should be a numeric string
	for _, c := range cfg.GroupID {
		if c < '0' || c > '9' {
			t.Errorf("GroupID %q is not a valid numeric string", cfg.GroupID)
			break
		}
	}

	// Verify not privileged for host mode
	if cfg.Privileged {
		t.Error("Privileged should be false for host mode")
	}
}

// TestResolveDockerDependency_DockerRuntime_DindMode verifies privileged mode
// is returned for docker:dind with Docker runtime.
func TestResolveDockerDependency_DockerRuntime_DindMode(t *testing.T) {
	depList := []deps.Dependency{
		{Name: "docker", DockerMode: deps.DockerModeDind},
	}

	cfg, err := ResolveDockerDependency(depList, container.RuntimeDocker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	// Verify dind config
	if cfg.Mode != deps.DockerModeDind {
		t.Errorf("Mode = %q, want %q", cfg.Mode, deps.DockerModeDind)
	}
	if !cfg.Privileged {
		t.Error("Privileged should be true for dind mode")
	}
	if cfg.GroupID != "" {
		t.Errorf("GroupID should be empty for dind mode, got %q", cfg.GroupID)
	}
	if cfg.SocketMount.Source != "" {
		t.Errorf("SocketMount should be empty for dind mode, got %+v", cfg.SocketMount)
	}
}

// TestResolveDockerDependency_NoModeErrors verifies that docker without explicit mode
// returns an error requiring the user to specify host or dind.
func TestResolveDockerDependency_NoModeErrors(t *testing.T) {
	depList := []deps.Dependency{
		{Name: "docker"}, // No explicit mode
	}

	_, err := ResolveDockerDependency(depList, container.RuntimeDocker)
	if err == nil {
		t.Fatal("expected error for docker without explicit mode")
	}
	if !strings.Contains(err.Error(), "requires explicit mode") {
		t.Errorf("error should mention 'requires explicit mode', got: %v", err)
	}
}

// TestGetDockerSocketGID verifies GID detection from the docker socket.
func TestGetDockerSocketGID(t *testing.T) {
	// Skip if docker socket doesn't exist
	if _, err := os.Stat(DockerSocketPath); os.IsNotExist(err) {
		t.Skipf("docker socket not available at %s", DockerSocketPath)
	}

	gid, err := GetDockerSocketGID()
	if err != nil {
		t.Fatalf("GetDockerSocketGID() error: %v", err)
	}

	// Verify GID matches what we get from a direct stat call
	info, err := os.Stat(DockerSocketPath)
	if err != nil {
		t.Fatalf("os.Stat() error: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("syscall.Stat_t not available on this platform")
	}
	if gid != stat.Gid {
		t.Errorf("GetDockerSocketGID() = %d, want %d", gid, stat.Gid)
	}
}

// TestGetDockerSocketGID_NotExists verifies error when socket doesn't exist.
func TestGetDockerSocketGID_NotExists(t *testing.T) {
	// Temporarily override the socket path
	originalPath := DockerSocketPath
	// Note: We can't actually modify the const, so this test verifies
	// the error handling by checking if the socket exists first.
	// If it doesn't exist, we verify the function returns an appropriate error.
	if _, err := os.Stat(originalPath); os.IsNotExist(err) {
		_, err := GetDockerSocketGID()
		if err == nil {
			t.Error("expected error when docker socket doesn't exist")
		}
	} else {
		// Socket exists, so we can't test the "not found" case directly.
		// This is fine - the test documents the expected behavior.
		t.Log("docker socket exists, skipping not-found test")
	}
}
