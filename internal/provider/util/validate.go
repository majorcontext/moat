package util

import (
	"fmt"
	"strings"
)

// ValidateTokenPrefix checks that a token has an expected prefix.
func ValidateTokenPrefix(token, prefix, tokenType string) error {
	if !strings.HasPrefix(token, prefix) {
		return fmt.Errorf("%s must start with %q", tokenType, prefix)
	}
	return nil
}

// ValidateTokenLength checks that a token has a minimum length.
func ValidateTokenLength(token string, minLen int, tokenType string) error {
	if len(token) < minLen {
		return fmt.Errorf("%s must be at least %d characters", tokenType, minLen)
	}
	return nil
}
