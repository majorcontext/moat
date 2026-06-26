package container

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAppleInspectLegacySchema(t *testing.T) {
	// Pre-1.0.0: status is a bare string, networks/image/created are top-level.
	const data = `[{
		"id": "run_abc",
		"name": "moat-run",
		"image": "moat/run:abc123",
		"created": "2026-01-02T03:04:05Z",
		"status": "running",
		"networks": [{"ipv4Address": "192.168.68.2/24", "ipv4Gateway": "192.168.68.1"}]
	}]`

	info, err := parseAppleInspect([]byte(data))
	require.NoError(t, err)
	require.Len(t, info, 1)

	c := info[0]
	assert.Equal(t, "running", c.state())
	assert.Equal(t, "moat/run:abc123", c.imageRef())
	assert.Equal(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), c.createdTime())

	nets := c.networks()
	require.Len(t, nets, 1)
	assert.Equal(t, "192.168.68.2/24", nets[0].IPv4Address)
}

func TestParseAppleInspectV1Schema(t *testing.T) {
	// container 1.0.0: status is an object, image/creation under configuration.
	const data = `[{
		"id": "run_abc",
		"name": "moat-run",
		"configuration": {
			"creationDate": "2026-01-02T03:04:05Z",
			"image": {"reference": "moat/run:abc123"}
		},
		"status": {
			"state": "running",
			"startedDate": "2026-01-02T03:04:06Z",
			"networks": [{"ipv4Address": "192.168.64.2/24", "ipv4Gateway": "192.168.64.1"}]
		}
	}]`

	info, err := parseAppleInspect([]byte(data))
	require.NoError(t, err)
	require.Len(t, info, 1)

	c := info[0]
	assert.Equal(t, "running", c.state())
	assert.Equal(t, "moat/run:abc123", c.imageRef())
	assert.Equal(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), c.createdTime())

	nets := c.networks()
	require.Len(t, nets, 1)
	assert.Equal(t, "192.168.64.2/24", nets[0].IPv4Address)
	assert.Equal(t, "192.168.64.1", nets[0].IPv4Gateway)
}

func TestParseAppleInspectStoppedV1(t *testing.T) {
	// A stopped 1.0.0 container has a status object with no networks.
	info, err := parseAppleInspect([]byte(`[{"id":"run_abc","status":{"networks":[],"state":"stopped"}}]`))
	require.NoError(t, err)
	require.Len(t, info, 1)
	assert.Equal(t, "stopped", info[0].state())
	assert.Empty(t, info[0].networks())
}

func TestParseAppleInspectEmptyAndNull(t *testing.T) {
	info, err := parseAppleInspect([]byte(`[]`))
	require.NoError(t, err)
	assert.Empty(t, info)

	// A null status must not error; it yields an empty state.
	info, err = parseAppleInspect([]byte(`[{"id":"run_abc","status":null}]`))
	require.NoError(t, err)
	require.Len(t, info, 1)
	assert.Equal(t, "", info[0].state())
}

func TestParseAppleInspectMalformed(t *testing.T) {
	_, err := parseAppleInspect([]byte(`not json`))
	assert.Error(t, err)
}
