package credential

import (
	"testing"
	"time"
)

func TestParseRoleARN(t *testing.T) {
	tests := []struct {
		name    string
		arn     string
		wantErr bool
	}{
		{
			name:    "valid arn",
			arn:     "arn:aws:iam::123456789012:role/AgentRole",
			wantErr: false,
		},
		{
			name:    "valid arn with path",
			arn:     "arn:aws:iam::123456789012:role/path/to/AgentRole",
			wantErr: false,
		},
		{
			name:    "invalid arn - not iam",
			arn:     "arn:aws:s3:::my-bucket",
			wantErr: true,
		},
		{
			name:    "invalid arn - bad format",
			arn:     "not-an-arn",
			wantErr: true,
		},
		{
			name:    "invalid arn - not a role",
			arn:     "arn:aws:iam::123456789012:user/MyUser",
			wantErr: true,
		},
		{
			name:    "empty arn",
			arn:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRoleARN(tt.arn)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRoleARN() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAWSConfig_SessionDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"15 minutes", "15m", 15 * time.Minute, false},
		{"1 hour", "1h", time.Hour, false},
		{"30 minutes", "30m", 30 * time.Minute, false},
		{"12 hours max", "12h", 12 * time.Hour, false},
		{"too short", "5m", 0, true},
		{"too long", "13h", 0, true},
		{"empty uses default", "", 15 * time.Minute, false},
		{"invalid format", "abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &AWSConfig{SessionDurationStr: tt.input}
			got, err := cfg.SessionDuration()
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionDuration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("SessionDuration() = %v, want %v", got, tt.expected)
			}
		})
	}
}
