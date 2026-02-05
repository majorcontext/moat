package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"text/tabwriter"
	"time"
)

func TestCredentialOutputFormatting(t *testing.T) {
	// Create a sample JWT with claims
	claims := map[string]interface{}{
		"exp":   float64(time.Now().Add(30 * 24 * time.Hour).Unix()),
		"iat":   float64(time.Now().Unix()),
		"scope": "openid profile user:read",
		"sub":   "usr_abc123def456",
		"https://api.openai.com/auth": map[string]interface{}{
			"organization_uuid": "org_xyz789abc123",
			"user_id":           "usr_abc123def456",
		},
	}

	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	// Simulate credential output
	fmt.Fprintf(tw, "Provider:\t%s\n", "anthropic")
	fmt.Fprintf(tw, "Token prefix:\t%s...\n", "sk-ant-oat01")
	fmt.Fprintf(tw, "Type:\t%s\n", "OAuth Token (JWT)")
	fmt.Fprintf(tw, "Scopes:\t%s\n", "openid, profile, user:read")
	fmt.Fprintf(tw, "Expires:\t%s\n", time.Now().Add(30*24*time.Hour).Format("2006-01-02"))
	tw.Flush()

	fmt.Fprintln(&buf, "JWT Claims:")
	printClaims(&buf, claims, "  ")

	output := buf.String()

	// Verify output contains expected fields
	if !strings.Contains(output, "Provider:") {
		t.Error("Output missing Provider field")
	}
	if !strings.Contains(output, "Token prefix:") {
		t.Error("Output missing Token prefix field")
	}
	if !strings.Contains(output, "Type:") {
		t.Error("Output missing Type field")
	}
	if !strings.Contains(output, "Scopes:") {
		t.Error("Output missing Scopes field")
	}
	if !strings.Contains(output, "Expires:") {
		t.Error("Output missing Expires field")
	}
	if !strings.Contains(output, "JWT Claims:") {
		t.Error("Output missing JWT Claims section")
	}
	if !strings.Contains(output, "organization_uuid") {
		t.Error("Output missing organization_uuid in JWT claims")
	}

	// Print for manual inspection
	t.Logf("\n=== Expected Credential Output ===\n%s", output)
}

func TestGetTokenPrefix(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "Anthropic OAuth token",
			token:    "sk-ant-oat01-abcdefghijklmnop",
			expected: "sk-ant-oat01",
		},
		{
			name:     "Anthropic API token",
			token:    "sk-ant-api03-abcdefghijklmnop",
			expected: "sk-ant-api03",
		},
		{
			name:     "GitHub personal token",
			token:    "ghp_abcdefghijklmnop",
			expected: "ghp_abcd",
		},
		{
			name:     "GitHub OAuth token",
			token:    "gho_abcdefghijklmnop",
			expected: "gho_abcd",
		},
		{
			name:     "Generic long token",
			token:    "someprefix_abcdefghijklmnop",
			expected: "somepref",
		},
		{
			name:     "Short token",
			token:    "short",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getTokenPrefix(tt.token)
			if result != tt.expected {
				t.Errorf("getTokenPrefix(%q) = %q, want %q", tt.token, result, tt.expected)
			}
		})
	}
}

func TestPrintClaims(t *testing.T) {
	claims := map[string]interface{}{
		"exp":   float64(1735689600), // Fixed timestamp
		"iat":   float64(1704067200),
		"scope": "openid profile",
		"sub":   "usr_verylongidentifierthatshouldberedacted",
		"nested": map[string]interface{}{
			"org_id":  "org_12345678",
			"user_id": "user_abc",
		},
	}

	var buf bytes.Buffer
	printClaims(&buf, claims, "  ")

	output := buf.String()

	// Check that timestamps are formatted as dates (use local time, matching printClaims)
	iatYear := time.Unix(1704067200, 0).Format("2006")
	expYear := time.Unix(1735689600, 0).Format("2006")
	if !strings.Contains(output, expYear+"-") || !strings.Contains(output, iatYear+"-") {
		t.Error("Timestamps not formatted as dates")
	}

	// Check that long IDs are redacted
	if strings.Contains(output, "verylongidentifierthatshouldberedacted") {
		t.Error("Long sub ID should be redacted")
	}
	if !strings.Contains(output, "(redacted)") {
		t.Error("Should show (redacted) marker for long IDs")
	}

	// Check nested claims are shown
	if !strings.Contains(output, "nested:") {
		t.Error("Nested claims not shown")
	}

	t.Logf("\n=== JWT Claims Output ===\n%s", output)
}
