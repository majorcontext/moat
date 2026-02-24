package langserver

import (
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	t.Run("go server", func(t *testing.T) {
		spec, ok := Get("go")
		if !ok {
			t.Fatal("go should be in the registry")
		}
		if spec.Name != "go" {
			t.Errorf("Name = %q, want %q", spec.Name, "go")
		}
		if spec.Plugin != "gopls-lsp@claude-plugins-official" {
			t.Errorf("Plugin = %q, want %q", spec.Plugin, "gopls-lsp@claude-plugins-official")
		}
		if len(spec.InstallDeps) != 1 || spec.InstallDeps[0] != "gopls" {
			t.Errorf("InstallDeps = %v, want [gopls]", spec.InstallDeps)
		}
	})

	t.Run("typescript server", func(t *testing.T) {
		spec, ok := Get("typescript")
		if !ok {
			t.Fatal("typescript should be in the registry")
		}
		if spec.Plugin != "typescript-lsp@claude-plugins-official" {
			t.Errorf("Plugin = %q, want %q", spec.Plugin, "typescript-lsp@claude-plugins-official")
		}
		if len(spec.Dependencies) != 1 || spec.Dependencies[0] != "node@20" {
			t.Errorf("Dependencies = %v, want [node@20]", spec.Dependencies)
		}
		if len(spec.InstallDeps) != 2 {
			t.Errorf("InstallDeps = %v, want 2 entries", spec.InstallDeps)
		}
	})

	t.Run("python server", func(t *testing.T) {
		spec, ok := Get("python")
		if !ok {
			t.Fatal("python should be in the registry")
		}
		if spec.Plugin != "pyright-lsp@claude-plugins-official" {
			t.Errorf("Plugin = %q, want %q", spec.Plugin, "pyright-lsp@claude-plugins-official")
		}
		if len(spec.InstallDeps) != 1 || spec.InstallDeps[0] != "npm:pyright" {
			t.Errorf("InstallDeps = %v, want [npm:pyright]", spec.InstallDeps)
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
		err := Validate([]string{"go", "typescript", "python"})
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
	t.Run("go dependencies", func(t *testing.T) {
		deps := AllDependencies([]string{"go"})
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
			t.Error("go dependencies should include 'go@1.25'")
		}
		if !hasGopls {
			t.Error("go dependencies should include 'gopls' (install dep)")
		}
	})

	t.Run("typescript dependencies", func(t *testing.T) {
		deps := AllDependencies([]string{"typescript"})
		hasNode := false
		hasTS := false
		hasTSServer := false
		for _, d := range deps {
			switch d {
			case "node@20":
				hasNode = true
			case "npm:typescript":
				hasTS = true
			case "npm:typescript-language-server":
				hasTSServer = true
			}
		}
		if !hasNode {
			t.Error("typescript dependencies should include 'node@20'")
		}
		if !hasTS {
			t.Error("typescript dependencies should include 'npm:typescript'")
		}
		if !hasTSServer {
			t.Error("typescript dependencies should include 'npm:typescript-language-server'")
		}
	})

	t.Run("python dependencies", func(t *testing.T) {
		deps := AllDependencies([]string{"python"})
		hasPython := false
		hasPyright := false
		for _, d := range deps {
			switch d {
			case "python":
				hasPython = true
			case "npm:pyright":
				hasPyright = true
			}
		}
		if !hasPython {
			t.Error("python dependencies should include 'python'")
		}
		if !hasPyright {
			t.Error("python dependencies should include 'npm:pyright'")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		deps := AllDependencies(nil)
		if len(deps) != 0 {
			t.Errorf("AllDependencies(nil) = %v, want empty", deps)
		}
	})

	t.Run("deduplication", func(t *testing.T) {
		deps := AllDependencies([]string{"go"})
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

func TestPlugins(t *testing.T) {
	t.Run("single server", func(t *testing.T) {
		plugins := Plugins([]string{"go"})
		if len(plugins) != 1 || plugins[0] != "gopls-lsp@claude-plugins-official" {
			t.Errorf("Plugins([go]) = %v, want [gopls-lsp@claude-plugins-official]", plugins)
		}
	})

	t.Run("multiple servers", func(t *testing.T) {
		plugins := Plugins([]string{"go", "typescript", "python"})
		if len(plugins) != 3 {
			t.Errorf("Plugins() returned %d plugins, want 3", len(plugins))
		}
	})

	t.Run("empty list", func(t *testing.T) {
		plugins := Plugins(nil)
		if len(plugins) != 0 {
			t.Errorf("Plugins(nil) = %v, want empty", plugins)
		}
	})

	t.Run("unknown server ignored", func(t *testing.T) {
		plugins := Plugins([]string{"nonexistent"})
		if len(plugins) != 0 {
			t.Errorf("Plugins(unknown) = %v, want empty", plugins)
		}
	})

	t.Run("deduplication", func(t *testing.T) {
		plugins := Plugins([]string{"go", "go"})
		if len(plugins) != 1 {
			t.Errorf("Plugins() with duplicate input returned %d, want 1", len(plugins))
		}
	})
}

func TestList(t *testing.T) {
	names := List()
	if len(names) != 3 {
		t.Errorf("List() returned %d names, want 3", len(names))
	}

	hasGo := false
	for _, n := range names {
		if n == "go" {
			hasGo = true
		}
	}
	if !hasGo {
		t.Error("List() should include go")
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
	if !strings.Contains(msg, "go") {
		t.Errorf("error should list available servers including go, got: %s", msg)
	}
	if !strings.Contains(msg, "Available language servers") {
		t.Errorf("error should say 'Available language servers', got: %s", msg)
	}
}

func TestValidate_MultipleNamesFirstBadStops(t *testing.T) {
	err := Validate([]string{"go", "nonexistent"})
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
	// Passing go twice should not duplicate its dependencies
	deps := AllDependencies([]string{"go", "go"})
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
	deps := AllDependencies([]string{"go", "unknown-lsp"})
	// go deps should still be returned; unknown is skipped
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

func TestGet_GoSpec_Complete(t *testing.T) {
	spec, ok := Get("go")
	if !ok {
		t.Fatal("go should be in the registry")
	}

	// Verify all required fields are populated
	if spec.Description == "" {
		t.Error("Description should not be empty")
	}
	if spec.Plugin == "" {
		t.Error("Plugin should not be empty")
	}
	if len(spec.Dependencies) == 0 {
		t.Error("Dependencies should not be empty")
	}
	// Dependencies should contain "go@1.25"
	hasGo := false
	for _, d := range spec.Dependencies {
		if d == "go@1.25" {
			hasGo = true
		}
	}
	if !hasGo {
		t.Error("go Dependencies should include 'go@1.25'")
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
	if !strings.Contains(result, "go") {
		t.Errorf("listNames() = %q, should contain 'go'", result)
	}
}

func TestAllDependencies_GoDependencyCount(t *testing.T) {
	deps := AllDependencies([]string{"go"})
	// go should have exactly 2 dependencies: "go@1.25" (from Dependencies) + "gopls" (InstallDeps)
	if len(deps) != 2 {
		t.Errorf("AllDependencies(go) = %v (len %d), want exactly 2 (go + gopls)", deps, len(deps))
	}
}
