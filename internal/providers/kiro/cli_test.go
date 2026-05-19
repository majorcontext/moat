package kiro

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func TestNetworkHosts(t *testing.T) {
	hosts := NetworkHosts()
	for _, want := range []string{"q.us-east-1.amazonaws.com", "cognito-identity.*.amazonaws.com", "cli.kiro.dev"} {
		if !slices.Contains(hosts, want) {
			t.Errorf("NetworkHosts() missing %q: %v", want, hosts)
		}
	}
}

func TestDefaultDependencies(t *testing.T) {
	deps := DefaultDependencies()
	if !slices.Contains(deps, "kiro-cli") || !slices.Contains(deps, "git") {
		t.Errorf("DefaultDependencies() = %v, want kiro-cli and git", deps)
	}
}

func TestRegisterCLIAddsKiroCommand(t *testing.T) {
	root := &cobra.Command{Use: "moat"}
	(&Provider{}).RegisterCLI(root)
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "kiro" {
			found = true
		}
	}
	if !found {
		t.Error("RegisterCLI did not add 'kiro' command")
	}
}
