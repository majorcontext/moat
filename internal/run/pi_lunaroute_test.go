package run

import "testing"

func TestMissingLunaRouteExtension(t *testing.T) {
	const ext = "npm:@lunaroute/pi-extension"
	tests := []struct {
		name  string
		hasPi bool
		grant []string
		pkgs  []string
		want  bool
	}{
		{"pi + grant, no extension", true, []string{"lunaroute"}, nil, true},
		{"pi + grant, other packages only", true, []string{"github", "lunaroute"}, []string{"npm:other"}, true},
		{"pi + grant + extension", true, []string{"lunaroute"}, []string{ext}, false},
		{"pi + grant + pinned extension", true, []string{"lunaroute"}, []string{ext + "@0.12.1"}, false},
		{"pi without lunaroute grant", true, []string{"anthropic"}, nil, false},
		{"grant without pi", false, []string{"lunaroute"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := missingLunaRouteExtension(tt.hasPi, tt.grant, tt.pkgs); got != tt.want {
				t.Errorf("missingLunaRouteExtension() = %v, want %v", got, tt.want)
			}
		})
	}
}
