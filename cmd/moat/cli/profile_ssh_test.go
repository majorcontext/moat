package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/agent"
)

func TestSSHRevokeAndRegrantProfile(t *testing.T) {
	for _, inherits := range []bool{false, true} {
		t.Run(fmt.Sprint(inherits), func(t *testing.T) {
			t.Setenv("MOAT_HOME", t.TempDir())
			t.Setenv("MOAT_KEYRING_BACKEND", "file")
			oldProfile, oldHost, oldKey := credential.ActiveProfile, sshHost, sshKeyPath
			t.Cleanup(func() { credential.ActiveProfile, sshHost, sshKeyPath = oldProfile, oldHost, oldKey })
			credential.ActiveProfile, sshHost, sshKeyPath = "client", "git.example", ""
			if err := config.CreateProfile("client", config.ProfileConfig{InheritDefault: inherits}); err != nil {
				t.Fatal(err)
			}
			key, err := credential.DefaultEncryptionKey()
			if err != nil {
				t.Fatal(err)
			}
			parent, err := credential.NewProfileStore("", key)
			if err != nil {
				t.Fatal(err)
			}
			child, err := credential.NewProfileStore("client", key)
			if err != nil {
				t.Fatal(err)
			}
			mapping := credential.SSHMapping{Host: sshHost, KeyFingerprint: "parent-key"}
			if err := parent.AddSSHMapping(mapping); err != nil {
				t.Fatal(err)
			}
			if !inherits {
				if err := child.AddSSHMapping(mapping); err != nil {
					t.Fatal(err)
				}
			}
			out := captureStdout(t, func() {
				if err := runRevoke(&cobra.Command{}, []string{"ssh:" + sshHost}); err != nil {
					t.Fatal(err)
				}
			})
			if strings.Contains(out, "Excluded from inheritance") != inherits {
				t.Fatalf("unexpected revoke output: %s", out)
			}
			if mappings, err := child.GetSSHMappings(); err != nil || len(mappings) != 0 {
				t.Fatalf("SSH grant still usable: %v, %v", mappings, err)
			}

			// Exercise the real grant command with a temporary SSH agent, so
			// clearing the exclusion is covered through both CLI branches.
			startProfileTestSSHAgent(t)
			captureStdout(t, func() {
				if err := runGrantSSH(&cobra.Command{}, nil); err != nil {
					t.Fatal(err)
				}
			})
			if excluded, err := child.IsExcluded(credential.Provider("ssh:" + sshHost)); err != nil || excluded {
				t.Fatalf("re-grant left exclusion: %v, %v", excluded, err)
			}
			if source, err := child.SSHSource(sshHost); err != nil || source != "client" {
				t.Fatalf("re-grant source = %s, %v", source, err)
			}
			mappings, err := parent.GetSSHMappings()
			if err != nil || len(mappings) != 1 || mappings[0].KeyFingerprint != "parent-key" {
				t.Fatalf("parent mapping changed: %v, %v", mappings, err)
			}
		})
	}
}

func startProfileTestSSHAgent(t *testing.T) {
	t.Helper()
	// Keep the Unix socket path below the OS length limit.
	dir, err := os.MkdirTemp("", "ssh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: privateKey}); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = agent.ServeAgent(keyring, conn)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	t.Setenv("SSH_AUTH_SOCK", socket)
}
