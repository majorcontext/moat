package daemon

import "testing"

func TestCredentialPublicationSkipsUnchangedEntries(t *testing.T) {
	rc := NewRunContext("test")
	if !rc.updateCredentialWithGrant("mcp.example", "Authorization", "token", "oauth:linear") {
		t.Fatal("first publication skipped")
	}
	if rc.updateCredentialWithGrant("mcp.example", "Authorization", "token", "oauth:linear") {
		t.Fatal("unchanged token rewritten")
	}
	if !rc.updateCredentialWithGrant("mcp.example", "Authorization", "rotated", "oauth:linear") {
		t.Fatal("shared parent rotation not published")
	}
	if !rc.updateCredentialWithGrant("mcp.example", "Authorization", "rotated", "oauth:sentry") {
		t.Fatal("different grant suppressed")
	}
	entries := rc.GetCredentials("mcp.example")
	if len(entries) != 2 || entries[0].Value != "rotated" {
		t.Fatalf("entries = %v", entries)
	}
}
