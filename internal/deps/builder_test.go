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
