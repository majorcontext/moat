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
	assert.Empty(t, info[0].state())
}

func TestParseAppleInspectMalformed(t *testing.T) {
	_, err := parseAppleInspect([]byte(`not json`))
	assert.Error(t, err)
}

func TestRunInfosFromInspect(t *testing.T) {
	// Mixed list across both schemas: a 1.0.0 run (no top-level name, run ID
	// carried as id), a legacy run (run ID in name), and two non-moat
	// containers that must be filtered out.
	const data = `[
		{"id":"run_aaaaaaaaaaaa","configuration":{"image":{"reference":"moat/run:1"}},"status":{"state":"running"}},
		{"id":"deadbeef","name":"run_bbbbbbbbbbbb","image":"moat/run:2","status":"stopped"},
		{"id":"buildkit","status":{"state":"running"}},
		{"id":"abc123","name":"some-other-container","status":"running"}
	]`

	info, err := parseAppleInspect([]byte(data))
	require.NoError(t, err)

	got := runInfosFromInspect(info)
	require.Len(t, got, 2, "only the two moat run containers should remain")

	// 1.0.0 entry: name falls back to id, image read from configuration.
	assert.Equal(t, "run_aaaaaaaaaaaa", got[0].ID)
	assert.Equal(t, "run_aaaaaaaaaaaa", got[0].Name)
	assert.Equal(t, "moat/run:1", got[0].Image)
	assert.Equal(t, "running", got[0].Status)

	// Legacy entry: name carries the run ID, id is the container hash.
	assert.Equal(t, "deadbeef", got[1].ID)
	assert.Equal(t, "run_bbbbbbbbbbbb", got[1].Name)
	assert.Equal(t, "moat/run:2", got[1].Image)
	assert.Equal(t, "stopped", got[1].Status)
}
