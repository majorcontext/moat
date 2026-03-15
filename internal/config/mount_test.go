package config

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMountEntryUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []MountEntry
		wantErr bool
	}{
		{
			name: "string form",
			yaml: `
- ./data:/data:ro
`,
			want: []MountEntry{
				{Source: "./data", Target: "/data", Mode: "ro", ReadOnly: true},
			},
		},
		{
			name: "object form with exclude",
			yaml: `
- source: .
  target: /workspace
  exclude:
    - node_modules
    - .venv
`,
			want: []MountEntry{
				{Source: ".", Target: "/workspace", Exclude: []string{"node_modules", ".venv"}},
			},
		},
		{
			name: "object form read-only",
			yaml: `
- source: ./data
  target: /data
  mode: ro
`,
			want: []MountEntry{
				{Source: "./data", Target: "/data", Mode: "ro", ReadOnly: true},
			},
		},
		{
			name: "mixed array",
			yaml: `
- ./data:/data:ro
- source: .
  target: /workspace
  exclude:
    - node_modules
`,
			want: []MountEntry{
				{Source: "./data", Target: "/data", Mode: "ro", ReadOnly: true},
				{Source: ".", Target: "/workspace", Exclude: []string{"node_modules"}},
			},
		},
		{
			name: "object form no exclude",
			yaml: `
- source: ./cache
  target: /cache
  mode: rw
`,
			want: []MountEntry{
				{Source: "./cache", Target: "/cache"},
			},
		},
		{
			name: "invalid mode",
			yaml: `
- source: .
  target: /workspace
  mode: readonly
`,
			wantErr: true,
		},
		{
			name: "rejects relative target in object form",
			yaml: `
- source: .
  target: relative/path
`,
			wantErr: true,
		},
		{
			name: "invalid yaml node type",
			yaml: `
- 42
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []MountEntry
			err := yaml.Unmarshal([]byte(tt.yaml), &got)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestValidateExcludes(t *testing.T) {
	tests := []struct {
		name     string
		excludes []string
		target   string
		want     []string
		wantErr  bool
	}{
		{
			name:     "valid single exclude",
			excludes: []string{"node_modules"},
			target:   "/workspace",
			want:     []string{"node_modules"},
		},
		{
			name:     "valid nested exclude",
			excludes: []string{"foo/bar/baz"},
			target:   "/workspace",
			want:     []string{"foo/bar/baz"},
		},
		{
			name:     "normalizes leading dot-slash",
			excludes: []string{"./node_modules"},
			target:   "/workspace",
			want:     []string{"node_modules"},
		},
		{
			name:     "normalizes trailing slash",
			excludes: []string{"node_modules/"},
			target:   "/workspace",
			want:     []string{"node_modules"},
		},
		{
			name:     "normalizes redundant separators",
			excludes: []string{"foo//bar"},
			target:   "/workspace",
			want:     []string{"foo/bar"},
		},
		{
			name:     "nil excludes",
			excludes: nil,
			target:   "/workspace",
			want:     nil,
		},
		{
			name:     "empty excludes",
			excludes: []string{},
			target:   "/workspace",
			want:     nil,
		},
		{
			name:     "rejects empty string",
			excludes: []string{""},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects dot-only",
			excludes: []string{"./"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects absolute path",
			excludes: []string{"/tmp/foo"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects dotdot",
			excludes: []string{"../foo"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects dotdot in middle",
			excludes: []string{"foo/../bar"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects duplicates",
			excludes: []string{"node_modules", "node_modules"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects overlapping excludes",
			excludes: []string{"foo", "foo/bar"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects overlapping excludes reverse order",
			excludes: []string{"foo/bar", "foo"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects normalized duplicates",
			excludes: []string{"node_modules", "./node_modules"},
			target:   "/workspace",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateExcludes(tt.excludes, tt.target)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseMount(t *testing.T) {
	tests := []struct {
		input    string
		source   string
		target   string
		readOnly bool
		wantErr  bool
	}{
		{"./data:/data", "./data", "/data", false, false},
		{"./data:/data:ro", "./data", "/data", true, false},
		{"/abs/path:/container", "/abs/path", "/container", false, false},
		{"./cache:/cache:rw", "./cache", "/cache", false, false},
		{"invalid", "", "", false, true},
		{"./data:relative", "", "", false, true},
		{"./data:/data:ro:extra", "", "", false, true},
		{"./data:/data:badmode", "", "", false, true},
		{":/data", "", "", false, true},
	}

	for _, tt := range tests {
		m, err := ParseMount(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseMount(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMount(%q): %v", tt.input, err)
			continue
		}
		if m.Source != tt.source {
			t.Errorf("ParseMount(%q) Source = %q, want %q", tt.input, m.Source, tt.source)
		}
		if m.Target != tt.target {
			t.Errorf("ParseMount(%q) Target = %q, want %q", tt.input, m.Target, tt.target)
		}
		if m.ReadOnly != tt.readOnly {
			t.Errorf("ParseMount(%q) ReadOnly = %v, want %v", tt.input, m.ReadOnly, tt.readOnly)
		}
	}
}
