// internal/deps/registry_test.go
package deps

import (
	"strings"
	"testing"
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
