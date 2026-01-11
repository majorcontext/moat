package config

import (
	"fmt"
	"strings"
)

// Mount represents a volume mount.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// ParseMount parses a mount string like "./data:/data:ro".
func ParseMount(s string) (*Mount, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid mount: %s (expected source:target[:ro])", s)
	}

	m := &Mount{
		Source: parts[0],
		Target: parts[1],
	}

	if len(parts) >= 3 && parts[2] == "ro" {
		m.ReadOnly = true
	}

	return m, nil
}
