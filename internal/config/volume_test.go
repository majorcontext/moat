package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerVolumeName(t *testing.T) {
	tests := []struct {
		agentName  string
		volumeName string
		want       string
	}{
		{"openclaw", "state", "moat_openclaw_state"},
		{"my-agent", "data", "moat_my-agent_data"},
		{"app", "cache-v2", "moat_app_cache-v2"},
	}
	for _, tt := range tests {
		got := DockerVolumeName(tt.agentName, tt.volumeName)
		if got != tt.want {
			t.Errorf("DockerVolumeName(%q, %q) = %q, want %q", tt.agentName, tt.volumeName, got, tt.want)
		}
	}
}

func TestVolumeDir(t *testing.T) {
	dir := VolumeDir("openclaw", "state")
	if !strings.Contains(dir, filepath.Join("volumes", "openclaw", "state")) {
		t.Errorf("VolumeDir() = %q, expected path containing volumes/openclaw/state", dir)
	}
}

func TestLoadConfigWithVolumes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test
volumes:
  - name: state
    target: /home/moatuser/.myapp
  - name: cache
    target: /var/cache/myapp
    readonly: true
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Volumes) != 2 {
		t.Fatalf("Volumes = %d, want 2", len(cfg.Volumes))
	}
	if cfg.Volumes[0].Name != "state" {
		t.Errorf("Volumes[0].Name = %q, want %q", cfg.Volumes[0].Name, "state")
	}
	if cfg.Volumes[0].Target != "/home/moatuser/.myapp" {
		t.Errorf("Volumes[0].Target = %q, want %q", cfg.Volumes[0].Target, "/home/moatuser/.myapp")
	}
	if cfg.Volumes[0].ReadOnly {
		t.Error("Volumes[0].ReadOnly should be false")
	}
	if cfg.Volumes[1].Name != "cache" {
		t.Errorf("Volumes[1].Name = %q, want %q", cfg.Volumes[1].Name, "cache")
	}
	if !cfg.Volumes[1].ReadOnly {
		t.Error("Volumes[1].ReadOnly should be true")
	}
}

func TestLoadConfigVolumesRequireName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
volumes:
  - name: state
    target: /data
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when volumes present without agent name")
	}
	if !strings.Contains(err.Error(), "'name' is required when volumes are configured") {
		t.Errorf("error should mention name requirement, got: %v", err)
	}
}

func TestLoadConfigVolumesValidation(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		errContains string
	}{
		{
			name: "missing volume name",
			content: `
name: myapp
agent: test
volumes:
  - target: /data
`,
			errContains: "'name' is required",
		},
		{
			name: "invalid volume name",
			content: `
name: myapp
agent: test
volumes:
  - name: INVALID
    target: /data
`,
			errContains: "invalid name",
		},
		{
			name: "volume name starts with hyphen",
			content: `
name: myapp
agent: test
volumes:
  - name: -bad
    target: /data
`,
			errContains: "invalid name",
		},
		{
			name: "missing target",
			content: `
name: myapp
agent: test
volumes:
  - name: state
`,
			errContains: "'target' is required",
		},
		{
			name: "relative target",
			content: `
name: myapp
agent: test
volumes:
  - name: state
    target: relative/path
`,
			errContains: "must be an absolute path",
		},
		{
			name: "duplicate volume names",
			content: `
name: myapp
agent: test
volumes:
  - name: state
    target: /data1
  - name: state
    target: /data2
`,
			errContains: "duplicate volume name",
		},
		{
			name: "duplicate volume targets",
			content: `
name: myapp
agent: test
volumes:
  - name: vol1
    target: /data
  - name: vol2
    target: /data
`,
			errContains: "duplicate volume target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "agent.yaml")
			os.WriteFile(configPath, []byte(tt.content), 0644)

			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load should error")
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error should contain %q, got: %v", tt.errContains, err)
			}
		})
	}
}

func TestLoadConfigVolumesValidNames(t *testing.T) {
	validNames := []string{"state", "data", "cache-v2", "my_volume", "a", "0data"}
	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "agent.yaml")
			content := `
name: myapp
agent: test
volumes:
  - name: ` + name + `
    target: /data
`
			os.WriteFile(configPath, []byte(content), 0644)

			_, err := Load(dir)
			if err != nil {
				t.Fatalf("Load should accept volume name %q, got error: %v", name, err)
			}
		})
	}
}
