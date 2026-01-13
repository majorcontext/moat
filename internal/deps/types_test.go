package deps

import "testing"

func TestDepSpecString(t *testing.T) {
	spec := DepSpec{
		Description: "Node.js runtime",
		Type:        TypeRuntime,
		Default:     "20",
	}
	if spec.Type != TypeRuntime {
		t.Errorf("Type = %v, want %v", spec.Type, TypeRuntime)
	}
}

func TestInstallTypeConstants(t *testing.T) {
	types := []InstallType{
		TypeRuntime,
		TypeGithubBinary,
		TypeApt,
		TypeNpm,
		TypePip,
		TypeCustom,
	}
	for _, typ := range types {
		if typ == "" {
			t.Error("InstallType should not be empty")
		}
	}
}
