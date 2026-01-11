package config

import "testing"

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
