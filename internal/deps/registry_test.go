// internal/deps/registry_test.go
package deps

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryLoaded(t *testing.T) {
	if len(AllSpecs()) == 0 {
		t.Fatal("Registry should not be empty")
	}
}

func TestRegistryHasNode(t *testing.T) {
	node, ok := GetSpec("node")
	if !ok {
		t.Fatal("Registry should have 'node'")
	}
	if node.Type != TypeRuntime {
		t.Errorf("node.Type = %v, want %v", node.Type, TypeRuntime)
	}
	if node.Default == "" {
		t.Error("node.Default should not be empty")
	}
}

func TestRegistryHasProtoc(t *testing.T) {
	protoc, ok := GetSpec("protoc")
	if !ok {
		t.Fatal("Registry should have 'protoc'")
	}
	if protoc.Type != TypeGithubBinary {
		t.Errorf("protoc.Type = %v, want %v", protoc.Type, TypeGithubBinary)
	}
	if protoc.Repo == "" {
		t.Error("protoc.Repo should not be empty")
	}
}

func TestRegistryHasPlaywright(t *testing.T) {
	pw, ok := GetSpec("playwright")
	if !ok {
		t.Fatal("Registry should have 'playwright'")
	}
	if pw.Type != TypeCustom {
		t.Errorf("playwright.Type = %v, want %v", pw.Type, TypeCustom)
	}
	if len(pw.Requires) == 0 || pw.Requires[0] != "node" {
		t.Errorf("playwright.Requires = %v, want [node]", pw.Requires)
	}
}

func TestServiceDepSpec(t *testing.T) {
	spec, ok := GetSpec("postgres")
	require.True(t, ok)
	assert.Equal(t, TypeService, spec.Type)
	assert.NotNil(t, spec.Service)
	assert.Equal(t, "postgres", spec.Service.Image)
	assert.Equal(t, 5432, spec.Service.Ports["default"])
	assert.Equal(t, "POSTGRES", spec.Service.EnvPrefix)
	assert.Equal(t, "pg_isready -h localhost -U postgres", spec.Service.ReadinessCmd)
}

// TestRegistryGithubBinaryPlaceholders validates that all github-binary entries
// with placeholders have proper substitution configured.
func TestRegistryGithubBinaryPlaceholders(t *testing.T) {
	for name, spec := range AllSpecs() {
		if spec.Type != TypeGithubBinary {
			continue
		}

		t.Run(name, func(t *testing.T) {
			// Check that {target} or {arch} placeholders have corresponding Targets map
			hasTargetPlaceholder := strings.Contains(spec.Asset, "{target}") || strings.Contains(spec.Bin, "{target}")
			hasArchPlaceholder := strings.Contains(spec.Asset, "{arch}") || strings.Contains(spec.Bin, "{arch}")

			if hasTargetPlaceholder || hasArchPlaceholder {
				// Must have Targets map OR legacy asset-arm64 field
				if len(spec.Targets) == 0 && spec.AssetARM64 == "" {
					t.Errorf("%s: has {target} or {arch} placeholder but no Targets map or AssetARM64", name)
				}

				// If Targets map exists, verify required architectures
				if len(spec.Targets) > 0 {
					if _, ok := spec.Targets["amd64"]; !ok {
						t.Errorf("%s: Targets map missing 'amd64' entry", name)
					}
					if _, ok := spec.Targets["arm64"]; !ok {
						t.Errorf("%s: Targets map missing 'arm64' entry", name)
					}
				}
			}

			// Verify generated commands don't contain unsubstituted placeholders
			if spec.Default != "" && len(spec.Targets) > 0 {
				cmds := getGithubBinaryCommands(name, spec.Default, spec)
				combined := strings.Join(cmds.Commands, " ")

				if strings.Contains(combined, "{version}") {
					t.Errorf("%s: generated command contains unsubstituted {version}", name)
				}
				if strings.Contains(combined, "{target}") {
					t.Errorf("%s: generated command contains unsubstituted {target}", name)
				}
				if strings.Contains(combined, "{arch}") {
					t.Errorf("%s: generated command contains unsubstituted {arch}", name)
				}
			}
		})
	}
}

// TestRegistryGithubBinaryURLsExist validates that all github-binary download URLs
// are reachable. This catches version/asset naming errors before e2e tests.
// Skipped by default; run with: go test -run TestRegistryGithubBinaryURLsExist -urls
func TestRegistryGithubBinaryURLsExist(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping URL validation in short mode")
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Follow redirects but limit to 10
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	for name, spec := range AllSpecs() {
		if spec.Type != TypeGithubBinary {
			continue
		}
		if spec.Default == "" {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			urls := getDownloadURLs(name, spec.Default, spec)
			for arch, url := range urls {
				t.Run(arch, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()

					req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
					if err != nil {
						t.Fatalf("failed to create request: %v", err)
					}

					resp, err := client.Do(req)
					if err != nil {
						t.Fatalf("failed to reach %s: %v", url, err)
					}
					defer resp.Body.Close()

					if resp.StatusCode == http.StatusNotFound {
						t.Errorf("URL returns 404: %s", url)
					} else if resp.StatusCode >= 400 {
						t.Errorf("URL returns %d: %s", resp.StatusCode, url)
					}
				})
			}
		})
	}
}

// getDownloadURLs returns the download URLs for a github-binary dependency.
func getDownloadURLs(name, version string, spec DepSpec) map[string]string {
	urls := make(map[string]string)

	if len(spec.Targets) > 0 {
		// New style with targets map
		for arch, target := range spec.Targets {
			asset := substituteAllPlaceholders(spec.Asset, version, target)
			urls[arch] = githubReleaseURL(spec.Repo, version, asset, spec.TagPrefix)
		}
	} else if spec.AssetARM64 != "" {
		// Legacy style
		amd64Asset := strings.ReplaceAll(spec.Asset, "{version}", version)
		arm64Asset := strings.ReplaceAll(spec.AssetARM64, "{version}", version)
		urls["amd64"] = githubReleaseURL(spec.Repo, version, amd64Asset, spec.TagPrefix)
		urls["arm64"] = githubReleaseURL(spec.Repo, version, arm64Asset, spec.TagPrefix)
	} else {
		// Single arch
		asset := strings.ReplaceAll(spec.Asset, "{version}", version)
		urls["amd64"] = githubReleaseURL(spec.Repo, version, asset, spec.TagPrefix)
	}

	return urls
}
