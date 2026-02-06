package provider

import (
	"errors"
	"strings"
	"testing"
)

func TestGrantError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *GrantError
		wantSub  string
		wantHint bool
	}{
		{
			name: "with hint",
			err: &GrantError{
				Provider: "github",
				Cause:    errors.New("token expired"),
				Hint:     "Run 'gh auth login' to refresh",
			},
			wantSub:  "grant github: token expired",
			wantHint: true,
		},
		{
			name: "without hint",
			err: &GrantError{
				Provider: "aws",
				Cause:    errors.New("role not found"),
			},
			wantSub:  "grant aws: role not found",
			wantHint: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("Error() = %q, want substring %q", got, tt.wantSub)
			}
			if tt.wantHint && !strings.Contains(got, tt.err.Hint) {
				t.Errorf("Error() = %q, should contain hint %q", got, tt.err.Hint)
			}
		})
	}
}

func TestGrantError_Unwrap(t *testing.T) {
	cause := errors.New("underlying error")
	err := &GrantError{
		Provider: "test",
		Cause:    cause,
	}

	unwrapped := err.Unwrap()
	if unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}

	// Verify errors.Is works
	if !errors.Is(err, cause) {
		t.Error("errors.Is() should match the cause")
	}
}
