package doctor

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// mockSection is a test implementation of Section
type mockSection struct {
	name   string
	output string
	err    error
}

func (m *mockSection) Name() string {
	return m.name
}

func (m *mockSection) Print(w io.Writer) error {
	if m.err != nil {
		return m.err
	}
	w.Write([]byte(m.output))
	return nil
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()

	// Initially empty
	if len(reg.Sections()) != 0 {
		t.Errorf("new registry should be empty, got %d sections", len(reg.Sections()))
	}

	// Register sections
	s1 := &mockSection{name: "section1", output: "output1\n"}
	s2 := &mockSection{name: "section2", output: "output2\n"}

	reg.Register(s1)
	reg.Register(s2)

	// Check sections are registered
	sections := reg.Sections()
	if len(sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(sections))
	}

	// Verify order is preserved
	if sections[0].Name() != "section1" {
		t.Errorf("first section name = %q, want %q", sections[0].Name(), "section1")
	}
	if sections[1].Name() != "section2" {
		t.Errorf("second section name = %q, want %q", sections[1].Name(), "section2")
	}
}

func TestSectionOutput(t *testing.T) {
	tests := []struct {
		name           string
		section        Section
		expectedOutput string
		expectError    bool
	}{
		{
			name:           "successful output",
			section:        &mockSection{name: "test", output: "hello world\n"},
			expectedOutput: "hello world\n",
			expectError:    false,
		},
		{
			name:           "empty output",
			section:        &mockSection{name: "test", output: ""},
			expectedOutput: "",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := tt.section.Print(&buf)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			output := buf.String()
			if output != tt.expectedOutput {
				t.Errorf("output = %q, want %q", output, tt.expectedOutput)
			}
		})
	}
}

func TestMultipleSections(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockSection{name: "Version", output: "v1.0.0\n"})
	reg.Register(&mockSection{name: "Runtime", output: "docker\n"})
	reg.Register(&mockSection{name: "Credentials", output: "2 credentials\n"})

	var buf bytes.Buffer
	for _, section := range reg.Sections() {
		buf.WriteString("# " + section.Name() + "\n")
		section.Print(&buf)
		buf.WriteString("\n")
	}

	output := buf.String()

	// Verify all sections appear in output
	if !strings.Contains(output, "# Version") {
		t.Error("output missing Version section")
	}
	if !strings.Contains(output, "# Runtime") {
		t.Error("output missing Runtime section")
	}
	if !strings.Contains(output, "# Credentials") {
		t.Error("output missing Credentials section")
	}

	// Verify content appears
	if !strings.Contains(output, "v1.0.0") {
		t.Error("output missing version content")
	}
	if !strings.Contains(output, "docker") {
		t.Error("output missing runtime content")
	}
	if !strings.Contains(output, "2 credentials") {
		t.Error("output missing credentials content")
	}
}
