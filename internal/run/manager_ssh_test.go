package run

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

// sshTestOpener returns an openCredStore-style closure yielding a fixed store
// and error, so setupSSHAgent's credential dependency can be faked in tests.
func sshTestOpener(store *credential.FileStore, err error) func() (*credential.FileStore, error) {
	return func() (*credential.FileStore, error) { return store, err }
}

// emptyTestStore returns a real, empty FileStore backed by a temp dir.
func emptyTestStore(t *testing.T) *credential.FileStore {
	t.Helper()
	store, err := credential.NewFileStore(t.TempDir(), make([]byte, 32))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

func TestSetupSSHAgent_NoGrants(t *testing.T) {
	m := &Manager{}
	setup, err := m.setupSSHAgent(&Run{}, Options{}, nil, "", sshTestOpener(nil, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setup.server != nil || setup.env != nil || setup.mounts != nil {
		t.Fatalf("expected zero-value setup for no grants, got %+v", setup)
	}
}

func TestSetupSSHAgent_MissingAuthSock(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	m := &Manager{}
	_, err := m.setupSSHAgent(&Run{}, Options{}, []string{"github.com"}, "", sshTestOpener(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Fatalf("expected SSH_AUTH_SOCK error, got %v", err)
	}
}

func TestSetupSSHAgent_CredStoreError(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	sentinel := errors.New("store open failed")
	m := &Manager{}
	_, err := m.setupSSHAgent(&Run{}, Options{}, []string{"github.com"}, "", sshTestOpener(nil, sentinel))
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the opener's error to propagate, got %v", err)
	}
}

func TestSetupSSHAgent_NoMappings(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	m := &Manager{}
	_, err := m.setupSSHAgent(&Run{}, Options{}, []string{"github.com"}, "", sshTestOpener(emptyTestStore(t), nil))
	if err == nil || !strings.Contains(err.Error(), "no SSH keys configured") {
		t.Fatalf("expected no-keys-configured error, got %v", err)
	}
}

func TestSetupSSHAgent_UpstreamConnectError(t *testing.T) {
	// A mapping exists for the host, so we get past the no-keys check, but
	// SSH_AUTH_SOCK points at a nonexistent socket, so connecting to the
	// upstream agent fails.
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "nonexistent.sock"))
	store := emptyTestStore(t)
	if err := store.AddSSHMapping(credential.SSHMapping{Host: "github.com", KeyFingerprint: "SHA256:test"}); err != nil {
		t.Fatalf("AddSSHMapping: %v", err)
	}
	m := &Manager{}
	_, err := m.setupSSHAgent(&Run{}, Options{}, []string{"github.com"}, "", sshTestOpener(store, nil))
	if err == nil || !strings.Contains(err.Error(), "connecting to SSH agent") {
		t.Fatalf("expected connecting-to-agent error, got %v", err)
	}
}
