package deps

import (
	"strings"
	"testing"
)

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

func TestValidate(t *testing.T) {
	tests := []struct {
		deps    []string
		wantErr bool
		errMsg  string
	}{
		// Valid cases
		{[]string{"node"}, false, ""},
		{[]string{"node", "typescript"}, false, ""},
		{[]string{"node@20", "yarn", "pnpm"}, false, ""},

		// Missing requirement
		{[]string{"typescript"}, true, "requires node"},
		{[]string{"playwright"}, true, "requires node"},
		{[]string{"yarn", "pnpm"}, true, "requires node"},

		// Unknown dependency
		{[]string{"unknown"}, true, "unknown dependency"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.deps, ","), func(t *testing.T) {
			deps, parseErr := ParseAll(tt.deps)
			var err error
			if parseErr == nil {
				err = Validate(deps)
			} else {
				err = parseErr
			}
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate(%v) should error", tt.deps)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate(%v) error: %v", tt.deps, err)
			}
		})
	}
}

func TestValidateSuggestion(t *testing.T) {
	_, err := ParseAll([]string{"nodejs"})
	if err == nil {
		t.Fatal("should error for nodejs")
	}
	if !strings.Contains(err.Error(), "node") {
		t.Errorf("error should suggest 'node', got: %v", err)
	}
}

func TestVersionValidation(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		errMsg  string
	}{
		// Valid versions
		{"node@20", false, ""},
		{"node@20.11", false, ""},
		{"go@1.22", false, ""},
		{"protoc@25.1", false, ""},
		{"python@3.11", false, ""},
		{"python@3_11", false, ""}, // underscore allowed

		// Invalid versions (shell injection attempts)
		{"node@20; rm -rf /", true, "invalid character"},
		{"node@$(whoami)", true, "invalid character"},
		{"node@`id`", true, "invalid character"},
		{"node@20|cat /etc/passwd", true, "invalid character"},
		{"node@20&echo pwned", true, "invalid character"},
		{"node@20\necho pwned", true, "invalid character"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Parse(%q) error: %v", tt.input, err)
			}
		})
	}
}

func TestVersionConstraintValidation(t *testing.T) {
	// Node has versions ["18", "20", "22"] in registry
	tests := []struct {
		deps    []string
		wantErr bool
		errMsg  string
	}{
		// Valid versions
		{[]string{"node@20"}, false, ""},
		{[]string{"node@18"}, false, ""},
		{[]string{"node@22"}, false, ""},
		{[]string{"node"}, false, ""}, // No version = use default

		// Invalid version for node (not in allowed list)
		{[]string{"node@16"}, true, "invalid version"},
		{[]string{"node@21"}, true, "invalid version"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.deps, ","), func(t *testing.T) {
			deps, parseErr := ParseAll(tt.deps)
			if parseErr != nil {
				t.Fatalf("ParseAll error: %v", parseErr)
			}
			err := Validate(deps)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate(%v) should error", tt.deps)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate(%v) error: %v", tt.deps, err)
			}
		})
	}
}
