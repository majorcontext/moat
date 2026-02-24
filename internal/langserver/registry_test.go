package langserver

import (
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	t.Run("known server", func(t *testing.T) {
		spec, ok := Get("gopls")
		if !ok {
			t.Fatal("gopls should be in the registry")
		}
		if spec.Name != "gopls" {
			t.Errorf("Name = %q, want %q", spec.Name, "gopls")
		}
		if spec.Command != "gopls" {
			t.Errorf("Command = %q, want %q", spec.Command, "gopls")
		}
		if len(spec.Args) != 1 || spec.Args[0] != "mcp" {
			t.Errorf("Args = %v, want [mcp]", spec.Args)
		}
		if spec.InstallDep != "gopls" {
			t.Errorf("InstallDep = %q, want %q", spec.InstallDep, "gopls")
		}
	})

	t.Run("unknown server", func(t *testing.T) {
		_, ok := Get("nonexistent")
		if ok {
			t.Error("nonexistent should not be in the registry")
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("valid names", func(t *testing.T) {
		err := Validate([]string{"gopls"})
		if err != nil {
			t.Errorf("Validate() error = %v, want nil", err)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		err := Validate(nil)
		if err != nil {
			t.Errorf("Validate(nil) error = %v, want nil", err)
		}
	})

	t.Run("unknown name", func(t *testing.T) {
		err := Validate([]string{"unknown-lsp"})
		if err == nil {
			t.Error("Validate() should return error for unknown server")
		}
	})
}

func TestAllDependencies(t *testing.T) {
	t.Run("gopls dependencies", func(t *testing.T) {
		deps := AllDependencies([]string{"gopls"})
		hasGo := false
		hasGopls := false
		for _, d := range deps {
			if d == "go@1.25" {
				hasGo = true
			}
			if d == "gopls" {
				hasGopls = true
			}
		}
		if !hasGo {
			t.Error("gopls dependencies should include 'go@1.25'")
		}
		if !hasGopls {
			t.Error("gopls dependencies should include 'gopls' (install dep)")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		deps := AllDependencies(nil)
		if len(deps) != 0 {
			t.Errorf("AllDependencies(nil) = %v, want empty", deps)
		}
	})

	t.Run("deduplication", func(t *testing.T) {
		// If we add a server that shares deps with gopls, they shouldn't duplicate
		deps := AllDependencies([]string{"gopls"})
		goCount := 0
		for _, d := range deps {
			if d == "go@1.25" {
				goCount++
			}
		}
		if goCount != 1 {
			t.Errorf("'go@1.25' appears %d times, want 1", goCount)
		}
	})
}

func TestMCPConfigs(t *testing.T) {
	t.Run("gopls config", func(t *testing.T) {
		configs := MCPConfigs([]string{"gopls"})
		cfg, ok := configs["gopls"]
		if !ok {
			t.Fatal("gopls should have an MCP config")
		}
		if cfg.Command != "gopls" {
			t.Errorf("Command = %q, want %q", cfg.Command, "gopls")
		}
		if len(cfg.Args) != 1 || cfg.Args[0] != "mcp" {
			t.Errorf("Args = %v, want [mcp]", cfg.Args)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		configs := MCPConfigs(nil)
		if len(configs) != 0 {
			t.Errorf("MCPConfigs(nil) = %v, want empty", configs)
		}
	})

	t.Run("unknown server ignored", func(t *testing.T) {
		configs := MCPConfigs([]string{"nonexistent"})
		if len(configs) != 0 {
			t.Errorf("MCPConfigs(unknown) = %v, want empty", configs)
		}
	})
}

func TestList(t *testing.T) {
	names := List()
	if len(names) == 0 {
		t.Error("List() should return at least one server")
	}

	hasGopls := false
	for _, n := range names {
		if n == "gopls" {
			hasGopls = true
		}
	}
	if !hasGopls {
		t.Error("List() should include gopls")
	}
}

func TestValidate_ErrorContainsAvailableNames(t *testing.T) {
	err := Validate([]string{"bogus-lsp"})
	if err == nil {
		t.Fatal("Validate() should return error for unknown server")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bogus-lsp") {
		t.Errorf("error should mention the unknown name, got: %s", msg)
	}
	if !strings.Contains(msg, "gopls") {
		t.Errorf("error should list available servers including gopls, got: %s", msg)
	}
	if !strings.Contains(msg, "Available language servers") {
		t.Errorf("error should say 'Available language servers', got: %s", msg)
	}
}

func TestValidate_MultipleNamesFirstBadStops(t *testing.T) {
	err := Validate([]string{"gopls", "nonexistent"})
	if err == nil {
		t.Fatal("Validate() should return error when any name is unknown")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention 'nonexistent', got: %s", err.Error())
	}
}

func TestAllDependencies_UnknownNameSkipped(t *testing.T) {
	deps := AllDependencies([]string{"nonexistent"})
	if len(deps) != 0 {
		t.Errorf("AllDependencies(unknown) = %v, want empty", deps)
	}
}

func TestAllDependencies_DuplicateInputs(t *testing.T) {
	// Passing gopls twice should not duplicate its dependencies
	deps := AllDependencies([]string{"gopls", "gopls"})
	goCount := 0
	goplsCount := 0
	for _, d := range deps {
		if d == "go@1.25" {
			goCount++
		}
		if d == "gopls" {
			goplsCount++
		}
	}
	if goCount != 1 {
		t.Errorf("'go@1.25' appears %d times with duplicate input, want 1", goCount)
	}
	if goplsCount != 1 {
		t.Errorf("'gopls' appears %d times with duplicate input, want 1", goplsCount)
	}
}

func TestAllDependencies_MixedKnownAndUnknown(t *testing.T) {
	deps := AllDependencies([]string{"gopls", "unknown-lsp"})
	// gopls deps should still be returned; unknown is skipped
	hasGo := false
	for _, d := range deps {
		if d == "go@1.25" {
			hasGo = true
		}
	}
	if !hasGo {
		t.Error("known server deps should be returned even with unknown in list")
	}
}

func TestMCPConfigs_MixedKnownAndUnknown(t *testing.T) {
	configs := MCPConfigs([]string{"gopls", "unknown-server"})
	if len(configs) != 1 {
		t.Errorf("MCPConfigs() returned %d configs, want 1 (only gopls)", len(configs))
	}
	if _, ok := configs["gopls"]; !ok {
		t.Error("gopls should be in configs")
	}
	if _, ok := configs["unknown-server"]; ok {
		t.Error("unknown-server should NOT be in configs")
	}
}

func TestGet_GoplsSpec_Complete(t *testing.T) {
	spec, ok := Get("gopls")
	if !ok {
		t.Fatal("gopls should be in the registry")
	}

	// Verify all required fields are populated
	if spec.Description == "" {
		t.Error("Description should not be empty")
	}
	if len(spec.Dependencies) == 0 {
		t.Error("Dependencies should not be empty")
	}
	// Dependencies should contain "go@1.25" (gopls needs Go >= 1.25)
	hasGo := false
	for _, d := range spec.Dependencies {
		if d == "go@1.25" {
			hasGo = true
		}
	}
	if !hasGo {
		t.Error("gopls Dependencies should include 'go@1.25'")
	}
}

func TestGet_EmptyString(t *testing.T) {
	_, ok := Get("")
	if ok {
		t.Error("empty string should not match any server")
	}
}

func TestListNames(t *testing.T) {
	result := listNames()
	if result == "" {
		t.Error("listNames() should not be empty")
	}
	if !strings.Contains(result, "gopls") {
		t.Errorf("listNames() = %q, should contain 'gopls'", result)
	}
}

func TestAllDependencies_GoplsDependencyCount(t *testing.T) {
	deps := AllDependencies([]string{"gopls"})
	// gopls should have exactly 2 dependencies: "go" (from Dependencies) + "gopls" (InstallDep)
	if len(deps) != 2 {
		t.Errorf("AllDependencies(gopls) = %v (len %d), want exactly 2 (go + gopls)", deps, len(deps))
	}
}
