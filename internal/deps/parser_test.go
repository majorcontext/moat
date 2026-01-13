package deps

import "testing"

func TestParseDependency(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		version string
		wantErr bool
	}{
		{"node", "node", "", false},
		{"node@20", "node", "20", false},
		{"node@20.11", "node", "20.11", false},
		{"protoc@25.1", "protoc", "25.1", false},
		{"golangci-lint@1.55.2", "golangci-lint", "1.55.2", false},
		{"", "", "", true},
		{"@20", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Name != tt.name {
				t.Errorf("Name = %q, want %q", dep.Name, tt.name)
			}
			if dep.Version != tt.version {
				t.Errorf("Version = %q, want %q", dep.Version, tt.version)
			}
		})
	}
}

func TestParseAll(t *testing.T) {
	deps, err := ParseAll([]string{"node@20", "protoc", "typescript"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if len(deps) != 3 {
		t.Fatalf("len(deps) = %d, want 3", len(deps))
	}
}

func TestParseAllUnknown(t *testing.T) {
	_, err := ParseAll([]string{"node", "unknowndep"})
	if err == nil {
		t.Error("ParseAll should error for unknown dependency")
	}
}
