package run

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestValidateReservedEnv(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		env     []string
		wantErr string // substring; "" = no error
	}{
		{"nil config, no env", nil, nil, ""},
		{"benign config env", &config.Config{Env: map[string]string{"FOO": "bar", "MOAT_LIKE_BUT_NOT": "x"}}, nil, ""},
		{"benign -e env", nil, []string{"FOO=bar", "BAZ"}, ""},
		{"MOAT_INIT_IMPL in config env", &config.Config{Env: map[string]string{"MOAT_INIT_IMPL": "go"}}, nil, "MOAT_INIT_IMPL is reserved"},
		{"MOAT_INIT_LEGACY in config env", &config.Config{Env: map[string]string{"MOAT_INIT_LEGACY": "1"}}, nil, "MOAT_INIT_LEGACY is reserved"},
		{"MOAT_INIT_IMPL in -e", nil, []string{"MOAT_INIT_IMPL=go"}, "MOAT_INIT_IMPL is reserved"},
		{"MOAT_INIT_LEGACY in -e", nil, []string{"MOAT_INIT_LEGACY=1"}, "MOAT_INIT_LEGACY is reserved"},
		// -e NAME without '=' is the host-passthrough form; it still injects
		// the variable, so it must be rejected too.
		{"bare -e passthrough", nil, []string{"MOAT_INIT_IMPL"}, "MOAT_INIT_IMPL is reserved"},
		// Case-insensitive, consistent with isMoatOwnedProxyVar.
		{"lowercase in -e", nil, []string{"moat_init_impl=go"}, "is reserved"},
		// Companion: an empty value is still an injection attempt.
		{"empty value in -e", nil, []string{"MOAT_INIT_IMPL="}, "MOAT_INIT_IMPL is reserved"},
		// Companion: prefix/suffix near-misses are not reserved.
		{"near-miss names pass", &config.Config{Env: map[string]string{"MOAT_INIT_IMPL_X": "1", "XMOAT_INIT_IMPL": "1"}}, []string{"MOAT_INIT=1"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReservedEnv(tt.cfg, tt.env)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateReservedEnv() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateReservedEnv() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestReservedInitVarsUnfiltered pins the division of labor: the reserved
// dispatcher vars are NOT part of the proxy-var filter (which only runs when
// a proxy is active and warns-and-skips). They must be rejected by
// validateReservedEnv regardless of proxy state, so adding them to
// isMoatOwnedProxyVar would silently weaken the guard.
func TestReservedInitVarsUnfiltered(t *testing.T) {
	for _, name := range reservedInitEnvVars {
		if isMoatOwnedProxyVar(name) {
			t.Errorf("%s is in isMoatOwnedProxyVar; it must stay under the always-on validateReservedEnv guard instead", name)
		}
		if !isReservedInitVar(name) {
			t.Errorf("isReservedInitVar(%s) = false", name)
		}
	}
}
