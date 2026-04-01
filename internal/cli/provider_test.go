package cli

import (
	"reflect"
	"testing"
)

func TestBuildGrants(t *testing.T) {
	tests := []struct {
		name         string
		autoDetected string
		configGrants []string
		flagGrants   []string
		want         []string
	}{
		{
			name:         "auto-detected claude with no explicit grants",
			autoDetected: "claude",
			want:         []string{"claude"},
		},
		{
			name:         "auto-detected claude suppressed when anthropic is explicit",
			autoDetected: "claude",
			flagGrants:   []string{"anthropic"},
			want:         []string{"anthropic"},
		},
		{
			name:         "auto-detected anthropic suppressed when claude is explicit",
			autoDetected: "anthropic",
			configGrants: []string{"claude"},
			want:         []string{"claude"},
		},
		{
			name:         "auto-detected gemini NOT suppressed when anthropic is explicit",
			autoDetected: "gemini",
			flagGrants:   []string{"anthropic"},
			want:         []string{"gemini", "anthropic"},
		},
		{
			name:         "auto-detected claude NOT suppressed when github is explicit",
			autoDetected: "claude",
			flagGrants:   []string{"github"},
			want:         []string{"claude", "github"},
		},
		{
			name:         "no auto-detected with explicit grants",
			autoDetected: "",
			configGrants: []string{"claude", "github"},
			want:         []string{"claude", "github"},
		},
		{
			name:         "deduplication across config and flag grants",
			autoDetected: "claude",
			configGrants: []string{"github"},
			flagGrants:   []string{"github", "claude"},
			want:         []string{"claude", "github"},
		},
		{
			name:         "both claude and anthropic explicit",
			autoDetected: "",
			flagGrants:   []string{"claude", "anthropic"},
			want:         []string{"claude", "anthropic"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildGrants(tt.autoDetected, tt.configGrants, tt.flagGrants)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildGrants(%q, %v, %v) = %v, want %v",
					tt.autoDetected, tt.configGrants, tt.flagGrants, got, tt.want)
			}
		})
	}
}
