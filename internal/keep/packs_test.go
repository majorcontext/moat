package keep

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStarterPack_Known(t *testing.T) {
	data, err := GetStarterPack("linear-readonly")
	require.NoError(t, err)
	assert.Contains(t, string(data), "scope:")
	assert.Contains(t, string(data), "rules:")
}

func TestGetStarterPack_Unknown(t *testing.T) {
	_, err := GetStarterPack("nonexistent-pack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown starter pack")
}

func TestListStarterPacks(t *testing.T) {
	packs := ListStarterPacks()
	assert.Contains(t, packs, "linear-readonly")
}
