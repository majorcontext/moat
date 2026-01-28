package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerRuntimeFeatureManagers(t *testing.T) {
	rt, err := NewDockerRuntime()
	require.NoError(t, err, "Docker runtime should be available in tests")
	defer rt.Close()

	assert.NotNil(t, rt.NetworkManager(), "Docker should provide NetworkManager")
	assert.NotNil(t, rt.SidecarManager(), "Docker should provide SidecarManager")
}

func TestAppleRuntimeNoFeatureManagers(t *testing.T) {
	rt, err := NewAppleRuntime()
	if err != nil {
		t.Skip("Apple containers not available")
	}
	defer rt.Close()

	assert.Nil(t, rt.NetworkManager(), "Apple should not provide NetworkManager")
	assert.Nil(t, rt.SidecarManager(), "Apple should not provide SidecarManager")
}
