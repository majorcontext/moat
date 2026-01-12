package name

import (
	"regexp"
	"testing"
)

func TestGenerate(t *testing.T) {
	name := Generate()

	// Should match adjective-animal pattern
	pattern := regexp.MustCompile(`^[a-z]+-[a-z]+$`)
	if !pattern.MatchString(name) {
		t.Errorf("Generate() = %q, want adjective-animal format", name)
	}
}

func TestGenerateUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := Generate()
		if seen[name] {
			t.Logf("Duplicate name after %d generations: %s", i, name)
		}
		seen[name] = true
	}
}
