package pi

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestResolvePiPreflightIgnoresUnselectedCredentials(t *testing.T) {
	origFlag, origModelFlag := piProviderFlag, piModelFlag
	origProvider, origModel := piResolvedProvider, piResolvedModel
	origProfile := credential.ActiveProfile
	t.Cleanup(func() {
		piProviderFlag, piModelFlag = origFlag, origModelFlag
		piResolvedProvider, piResolvedModel = origProvider, origModel
		credential.ActiveProfile = origProfile
	})
	credential.ActiveProfile = ""
	piModelFlag = ""
	t.Setenv("MOAT_KEYRING_BACKEND", "file")

	for _, backend := range supportedBackends {
		for _, source := range []string{"flag", "config", "flag overrides config"} {
			t.Run(backend+"/"+source, func(t *testing.T) {
				t.Setenv("MOAT_HOME", t.TempDir())
				key, err := credential.DefaultEncryptionKey()
				if err != nil {
					t.Fatal(err)
				}
				store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if err != nil {
					t.Fatal(err)
				}
				wrongKey := bytes.Clone(key)
				wrongKey[0] ^= 1
				unreadableStore, err := credential.NewFileStore(credential.DefaultStoreDir(), wrongKey)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range supportedBackends {
					target := unreadableStore
					if name == backend {
						target = store
					}
					if err := target.Save(credential.Credential{Provider: credential.Provider(name), Token: "test-key"}); err != nil {
						t.Fatal(err)
					}
				}

				piProviderFlag = ""
				cfg := &config.Config{}
				cfg.Pi.Model = "test-model"
				switch source {
				case "flag":
					piProviderFlag = backend
				case "config":
					cfg.Pi.Provider = backend
				case "flag overrides config":
					piProviderFlag = backend
					for _, other := range supportedBackends {
						if other != backend {
							cfg.Pi.Provider = other
							break
						}
					}
				}
				if err := resolvePiPreflight(cfg); err != nil {
					t.Fatalf("unrelated unreadable credentials blocked %s: %v", backend, err)
				}
				if piResolvedProvider != backend || piResolvedModel != "test-model" {
					t.Fatalf("resolved (%q, %q), want (%q, test-model)", piResolvedProvider, piResolvedModel, backend)
				}
			})
		}
	}
}

func TestPiGrantsFromStoreSelectedCredentialErrors(t *testing.T) {
	for _, backend := range supportedBackends {
		for _, cause := range []error{credential.ErrDecrypt, credential.ErrNotFound} {
			t.Run(backend+"/"+cause.Error(), func(t *testing.T) {
				var reads []string
				grants, err := piGrantsFromStore(backend, func(p credential.Provider) (*credential.Credential, error) {
					reads = append(reads, string(p))
					return nil, fmt.Errorf("test credential: %w", cause)
				})
				if !slices.Equal(reads, []string{backend}) {
					t.Fatalf("read credentials %v, want only %s", reads, backend)
				}
				if errors.Is(cause, credential.ErrNotFound) {
					if err != nil {
						t.Fatal(err)
					}
					_, _, err = resolvePiProvider(backend, "", grants)
					if err == nil || !strings.Contains(err.Error(), "moat grant "+backend) {
						t.Fatalf("missing selected credential: got %v", err)
					}
				} else if !errors.Is(err, cause) || !strings.Contains(err.Error(), "reading the "+backend+" credential") {
					t.Fatalf("selected credential error lost: got %v, want %v", err, cause)
				}
			})
		}
	}
}

func TestPiGrantsFromStoreSelectedAnthropicGateway(t *testing.T) {
	const gateway = "https://gw.lunaroute.com"
	grants, err := piGrantsFromStore(backendAnthropic, func(p credential.Provider) (*credential.Credential, error) {
		if p != credential.ProviderAnthropic {
			t.Fatalf("read unrelated credential %s", p)
		}
		return &credential.Credential{Metadata: map[string]string{credential.MetaKeyBaseURL: gateway}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = resolvePiProvider(backendAnthropic, "", grants)
	if err == nil || !strings.Contains(err.Error(), "moat grant lunaroute") {
		t.Fatalf("selected gateway credential: got %v, want LunaRoute guidance", err)
	}
}
