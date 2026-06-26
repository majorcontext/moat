package container

import (
	"bytes"
	"encoding/json"
	"time"
)

// The Apple `container` CLI changed the schema of its `inspect` and
// `list --format json` output between releases. Older versions emitted a
// top-level `status` string ("running") alongside a top-level `networks`
// array, an `image` string, and a `created` timestamp. The container 1.0.0
// release nests these: `status` became an object with `state` and `networks`
// fields, and image/creation metadata moved under `configuration`.
//
// The types and accessors below parse both schemas so moat works across CLI
// versions.

// appleNetwork is a single network attachment from `container inspect`.
type appleNetwork struct {
	IPv4Address string `json:"ipv4Address"`
	IPv4Gateway string `json:"ipv4Gateway"`
}

// appleStatus captures the container status. It accepts both the legacy string
// form ("running") and the container 1.0.0 object form
// ({"state":"running","networks":[...]}).
type appleStatus struct {
	State    string
	Networks []appleNetwork
}

// UnmarshalJSON decodes either the legacy string status or the 1.0.0 object.
func (s *appleStatus) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}

	// Legacy form: a bare JSON string, e.g. "running".
	if trimmed[0] == '"' {
		return json.Unmarshal(trimmed, &s.State)
	}

	// container 1.0.0 form: an object with state and networks.
	var obj struct {
		State    string         `json:"state"`
		Networks []appleNetwork `json:"networks"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return err
	}
	s.State = obj.State
	s.Networks = obj.Networks
	return nil
}

// appleInspectInfo is the subset of a `container inspect` / `container list
// --format json` entry that moat consumes. Field locations differ across CLI
// releases; the accessor methods normalize the two known schemas.
type appleInspectInfo struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Status appleStatus `json:"status"`

	// Legacy top-level fields (pre-1.0.0).
	LegacyNetworks []appleNetwork `json:"networks"`
	LegacyImage    string         `json:"image"`
	LegacyCreated  string         `json:"created"`

	// container 1.0.0 nests image and creation metadata under configuration.
	Configuration struct {
		CreationDate string `json:"creationDate"`
		Image        struct {
			Reference string `json:"reference"`
		} `json:"image"`
	} `json:"configuration"`
}

// state returns the container state ("running", "stopped", "exited", ...).
func (i appleInspectInfo) state() string { return i.Status.State }

// networks returns the network attachments, preferring the 1.0.0 location
// (status.networks) and falling back to the legacy top-level array.
// Note: the priority is inverted relative to imageRef because the 1.0.0 schema
// moved networks into status, whereas it moved image into configuration (not
// status) — so each accessor prefers wherever its field lives in 1.0.0.
func (i appleInspectInfo) networks() []appleNetwork {
	if len(i.Status.Networks) > 0 {
		return i.Status.Networks
	}
	return i.LegacyNetworks
}

// imageRef returns the image reference from either schema.
func (i appleInspectInfo) imageRef() string {
	if i.LegacyImage != "" {
		return i.LegacyImage
	}
	return i.Configuration.Image.Reference
}

// createdTime parses the creation timestamp from either schema. Returns the
// zero time if neither field is present or parseable.
func (i appleInspectInfo) createdTime() time.Time {
	raw := i.LegacyCreated
	if raw == "" {
		raw = i.Configuration.CreationDate
	}
	t, _ := time.Parse(time.RFC3339, raw)
	return t
}

// parseAppleInspect decodes the array returned by `container inspect` /
// `container list --format json` into the normalized info type.
func parseAppleInspect(data []byte) ([]appleInspectInfo, error) {
	var info []appleInspectInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return info, nil
}
