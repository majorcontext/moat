package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterServices(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "22", Type: TypeRuntime},
		{Name: "postgres", Version: "17", Type: TypeService},
		{Name: "redis", Version: "7", Type: TypeService},
	}

	services := FilterServices(deps)
	assert.Len(t, services, 2)
	assert.Equal(t, "postgres", services[0].Name)
	assert.Equal(t, "redis", services[1].Name)
}

func TestFilterInstallable(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "22", Type: TypeRuntime},
		{Name: "postgres", Version: "17", Type: TypeService},
		{Name: "redis", Version: "7", Type: TypeService},
	}

	installable := FilterInstallable(deps)
	assert.Len(t, installable, 1)
	assert.Equal(t, "node", installable[0].Name)
}

func TestFilterServicesEmpty(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "22", Type: TypeRuntime},
	}

	services := FilterServices(deps)
	assert.Empty(t, services)

	installable := FilterInstallable(deps)
	assert.Len(t, installable, 1)
}
