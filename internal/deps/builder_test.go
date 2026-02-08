// internal/deps/builder_test.go
package deps

import (
	"strings"
	"testing"
)

func TestImageTag(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
	}
	tag := ImageTag(deps, nil)
	if !strings.HasPrefix(tag, "moat/run:") {
		t.Errorf("tag should start with moat/run:, got %s", tag)
	}
	// Tag should be deterministic
	tag2 := ImageTag(deps, nil)
	if tag != tag2 {
		t.Errorf("tags should be equal: %s != %s", tag, tag2)
	}
}

func TestImageTagDifferent(t *testing.T) {
	tag1 := ImageTag([]Dependency{{Name: "node", Version: "20"}}, nil)
	tag2 := ImageTag([]Dependency{{Name: "node", Version: "22"}}, nil)
	if tag1 == tag2 {
		t.Error("different deps should have different tags")
	}
}

func TestImageTagOrderIndependent(t *testing.T) {
	deps1 := []Dependency{{Name: "node"}, {Name: "protoc"}}
	deps2 := []Dependency{{Name: "protoc"}, {Name: "node"}}
	tag1 := ImageTag(deps1, nil)
	tag2 := ImageTag(deps2, nil)
	if tag1 != tag2 {
		t.Errorf("order should not matter: %s != %s", tag1, tag2)
	}
}

func TestImageTagWithSSH(t *testing.T) {
	deps := []Dependency{{Name: "node"}}
	tagWithoutSSH := ImageTag(deps, nil)
	tagWithSSH := ImageTag(deps, &ImageTagOptions{NeedsSSH: true})
	if tagWithoutSSH == tagWithSSH {
		t.Error("SSH option should affect tag")
	}
}

func TestImageTagWithHooks(t *testing.T) {
	noHooks := ImageTag(nil, nil)
	withHooks := ImageTag(nil, &ImageTagOptions{
		Hooks: &HooksConfig{
			PostBuild:     "git config --global core.autocrlf input",
			PostBuildRoot: "apt-get install -y figlet",
		},
	})
	if noHooks == withHooks {
		t.Error("hooks should change the image hash")
	}

	// Different hooks should produce different tags
	hooks1 := ImageTag(nil, &ImageTagOptions{
		Hooks: &HooksConfig{PostBuild: "echo a"},
	})
	hooks2 := ImageTag(nil, &ImageTagOptions{
		Hooks: &HooksConfig{PostBuild: "echo b"},
	})
	if hooks1 == hooks2 {
		t.Error("different hooks should produce different image tags")
	}

	// pre_run should also affect hash
	withPreRun := ImageTag(nil, &ImageTagOptions{
		Hooks: &HooksConfig{PreRun: "npm install"},
	})
	if noHooks == withPreRun {
		t.Error("pre_run should change the image hash")
	}
}

func TestImageTagDockerModes(t *testing.T) {
	// docker:host and docker:dind should produce different image tags
	// because they install different packages (CLI-only vs full daemon)
	hostDeps := []Dependency{{Name: "docker", DockerMode: DockerModeHost}}
	dindDeps := []Dependency{{Name: "docker", DockerMode: DockerModeDind}}

	hostTag := ImageTag(hostDeps, nil)
	dindTag := ImageTag(dindDeps, nil)

	if hostTag == dindTag {
		t.Errorf("docker:host and docker:dind should have different tags, both got: %s", hostTag)
	}
}
