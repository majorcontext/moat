package util

import (
	"testing"
)

func TestValidateTokenPrefix(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		prefix    string
		tokenType string
		wantErr   bool
	}{
		{
			name:      "valid prefix",
			token:     "ghp_abc123",
			prefix:    "ghp_",
			tokenType: "GitHub token",
			wantErr:   false,
		},
		{
			name:      "invalid prefix",
			token:     "sk-abc123",
			prefix:    "ghp_",
			tokenType: "GitHub token",
			wantErr:   true,
		},
		{
			name:      "empty token",
			token:     "",
			prefix:    "ghp_",
			tokenType: "GitHub token",
			wantErr:   true,
		},
		{
			name:      "empty prefix matches all",
			token:     "anything",
			prefix:    "",
			tokenType: "token",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTokenPrefix(tt.token, tt.prefix, tt.tokenType)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTokenPrefix() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTokenLength(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		minLen    int
		tokenType string
		wantErr   bool
	}{
		{
			name:      "valid length",
			token:     "abcdefghij",
			minLen:    10,
			tokenType: "token",
			wantErr:   false,
		},
		{
			name:      "too short",
			token:     "abc",
			minLen:    10,
			tokenType: "token",
			wantErr:   true,
		},
		{
			name:      "exact length",
			token:     "abc",
			minLen:    3,
			tokenType: "token",
			wantErr:   false,
		},
		{
			name:      "empty token",
			token:     "",
			minLen:    1,
			tokenType: "token",
			wantErr:   true,
		},
		{
			name:      "zero min length",
			token:     "",
			minLen:    0,
			tokenType: "token",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTokenLength(tt.token, tt.minLen, tt.tokenType)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTokenLength() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
