package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/spf13/cobra"
)

func TestRevokeUnreadableCredential(t *testing.T) {
	for _, scope := range []string{"default", "inherited", "local override"} {
		for _, damage := range []string{"wrong key", "truncated"} {
			t.Run(scope+"/"+damage, func(t *testing.T) {
				t.Setenv("MOAT_HOME", t.TempDir())
				oldProfile := credential.ActiveProfile
				t.Cleanup(func() { credential.ActiveProfile = oldProfile })
				credential.ActiveProfile = ""
				if scope != "default" {
					credential.ActiveProfile = "client"
					if err := config.CreateProfile("client", config.ProfileConfig{InheritDefault: true}); err != nil {
						t.Fatal(err)
					}
				}
				key, err := credential.DefaultEncryptionKey()
				if err != nil {
					t.Fatal(err)
				}
				parent, err := credential.NewProfileStore("", key)
				if err != nil {
					t.Fatal(err)
				}
				if err := parent.Save(credential.Credential{Provider: credential.ProviderAnthropic, Token: "parent-key"}); err != nil {
					t.Fatal(err)
				}
				store, err := credential.NewProfileStore(credential.ActiveProfile, key)
				if err != nil {
					t.Fatal(err)
				}
				damagedDir := credential.StoreDirForProfile("")
				if scope == "local override" {
					damagedDir = credential.DefaultStoreDir()
				}
				if damage == "wrong key" {
					wrongKey := bytes.Clone(key)
					wrongKey[0] ^= 1
					wrongStore, err := credential.NewFileStore(damagedDir, wrongKey)
					if err != nil {
						t.Fatal(err)
					}
					if err := wrongStore.Save(credential.Credential{Provider: credential.ProviderAnthropic, Token: "old-key"}); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(filepath.Join(damagedDir, "anthropic.enc"), []byte("truncated"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Get(credential.ProviderAnthropic); err == nil || errors.Is(err, credential.ErrNotFound) {
					t.Fatalf("expected unreadable credential, got %v", err)
				}
				parentPath := filepath.Join(credential.StoreDirForProfile(""), "anthropic.enc")
				parentBefore, err := os.ReadFile(parentPath)
				if err != nil {
					t.Fatal(err)
				}

				if err := runRevoke(&cobra.Command{}, []string{"anthropic"}); err != nil {
					t.Fatalf("revoke unreadable credential: %v", err)
				}
				if _, err := store.Get(credential.ProviderAnthropic); !errors.Is(err, credential.ErrNotFound) {
					t.Fatalf("credential still available: %v", err)
				}
				if _, err := os.Stat(filepath.Join(credential.DefaultStoreDir(), "anthropic.enc")); !os.IsNotExist(err) {
					t.Fatalf("local credential not removed: %v", err)
				}
				if scope != "default" {
					parentAfter, err := os.ReadFile(parentPath)
					if err != nil || !bytes.Equal(parentBefore, parentAfter) {
						t.Fatalf("parent credential changed: %v", err)
					}
					excluded, err := store.IsExcluded(credential.ProviderAnthropic)
					if err != nil || !excluded {
						t.Fatalf("grant not excluded from inheritance: %v", err)
					}
				}
			})
		}
	}
}

func TestRevokeMissingCredential(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	oldProfile := credential.ActiveProfile
	t.Cleanup(func() { credential.ActiveProfile = oldProfile })
	credential.ActiveProfile = ""
	if err := runRevoke(&cobra.Command{}, []string{"anthropic"}); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("expected missing credential error, got %v", err)
	}
}
