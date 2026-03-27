package keep

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"
)

//go:embed packs/*.yaml
var packsFS embed.FS

// GetStarterPack returns the YAML bytes for a named starter pack.
func GetStarterPack(name string) ([]byte, error) {
	filename := name + ".yaml"
	data, err := packsFS.ReadFile(filepath.Join("packs", filename))
	if err != nil {
		return nil, fmt.Errorf("unknown starter pack %q — available packs: %s", name, strings.Join(ListStarterPacks(), ", "))
	}
	return data, nil
}

// ListStarterPacks returns the names of all available starter packs.
func ListStarterPacks() []string {
	entries, err := packsFS.ReadDir("packs")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
		}
	}
	return names
}
