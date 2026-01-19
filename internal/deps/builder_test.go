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
	tag := ImageTag(deps)
	if !strings.HasPrefix(tag, "moat/run:") {
		t.Errorf("tag should start with moat/run:, got %s", tag)
	}
	// Tag should be deterministic
	tag2 := ImageTag(deps)
	if tag != tag2 {
		t.Errorf("tags should be equal: %s != %s", tag, tag2)
	}
}

func TestImageTagDifferent(t *testing.T) {
	tag1 := ImageTag([]Dependency{{Name: "node", Version: "20"}})
	tag2 := ImageTag([]Dependency{{Name: "node", Version: "22"}})
	if tag1 == tag2 {
		t.Error("different deps should have different tags")
	}
}

func TestImageTagOrderIndependent(t *testing.T) {
	deps1 := []Dependency{{Name: "node"}, {Name: "protoc"}}
	deps2 := []Dependency{{Name: "protoc"}, {Name: "node"}}
	tag1 := ImageTag(deps1)
	tag2 := ImageTag(deps2)
	if tag1 != tag2 {
		t.Errorf("order should not matter: %s != %s", tag1, tag2)
	}
}
