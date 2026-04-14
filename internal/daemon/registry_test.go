package daemon

import (
	"net/http"
	"testing"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewRegistry()
	rc := &RunContext{RunID: "run-1"}

	token := reg.Register(rc)
	if token == "" {
		t.Fatal("Register returned empty token")
	}

	got, ok := reg.Lookup(token)
	if !ok {
		t.Fatal("Lookup returned not found for registered token")
	}
	if got.RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", got.RunID, "run-1")
	}
	if got.AuthToken != token {
		t.Errorf("AuthToken = %q, want %q", got.AuthToken, token)
	}

	// Lookup with invalid token should fail.
	_, ok = reg.Lookup("nonexistent-token")
	if ok {
		t.Error("Lookup returned ok for nonexistent token")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewRegistry()
	rc := &RunContext{RunID: "run-1"}

	token := reg.Register(rc)

	// Verify it exists.
	if _, ok := reg.Lookup(token); !ok {
		t.Fatal("Lookup failed before Unregister")
	}

	reg.Unregister(token)

	// Verify it no longer exists.
	if _, ok := reg.Lookup(token); ok {
		t.Error("Lookup returned ok after Unregister")
	}

	// Count should be zero.
	if n := reg.Count(); n != 0 {
		t.Errorf("Count = %d, want 0", n)
	}
}

func TestRegistry_List(t *testing.T) {
	reg := NewRegistry()
	rc1 := &RunContext{RunID: "run-1"}
	rc2 := &RunContext{RunID: "run-2"}

	reg.Register(rc1)
	reg.Register(rc2)

	list := reg.List()
	if len(list) != 2 {
		t.Fatalf("List returned %d items, want 2", len(list))
	}

	ids := make(map[string]bool)
	for _, rc := range list {
		ids[rc.RunID] = true
	}
	if !ids["run-1"] {
		t.Error("List missing run-1")
	}
	if !ids["run-2"] {
		t.Error("List missing run-2")
	}
}

func TestRegistry_UpdateContainerID(t *testing.T) {
	reg := NewRegistry()
	rc := &RunContext{RunID: "run-1"}

	token := reg.Register(rc)

	ok := reg.UpdateContainerID(token, "container-abc123")
	if !ok {
		t.Fatal("UpdateContainerID returned false for registered token")
	}

	got, _ := reg.Lookup(token)
	if got.ContainerID != "container-abc123" {
		t.Errorf("ContainerID = %q, want %q", got.ContainerID, "container-abc123")
	}

	// Update with nonexistent token should return false.
	ok = reg.UpdateContainerID("bad-token", "container-xyz")
	if ok {
		t.Error("UpdateContainerID returned true for nonexistent token")
	}
}

func TestRegistry_UniqueTokens(t *testing.T) {
	reg := NewRegistry()
	rc1 := &RunContext{RunID: "run-1"}
	rc2 := &RunContext{RunID: "run-2"}

	token1 := reg.Register(rc1)
	token2 := reg.Register(rc2)

	if token1 == token2 {
		t.Errorf("Register produced duplicate tokens: %q", token1)
	}

	// Each token should be 64 hex characters (32 bytes).
	if len(token1) != 64 {
		t.Errorf("token1 length = %d, want 64", len(token1))
	}
	if len(token2) != 64 {
		t.Errorf("token2 length = %d, want 64", len(token2))
	}
}

func dummyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRegistry_FindGCloudHandler_SingleRun(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run-1")
	rc.GCloudConfig = &GCloudConfig{Profile: "dev"}
	rc.SetGCloudHandler(dummyHandler())
	reg.Register(rc)

	if h := reg.FindGCloudHandler(); h == nil {
		t.Error("FindGCloudHandler returned nil for single gcloud run")
	}
}

func TestRegistry_FindGCloudHandler_SameProfile(t *testing.T) {
	reg := NewRegistry()

	rc1 := NewRunContext("run-1")
	rc1.GCloudConfig = &GCloudConfig{Profile: "dev"}
	rc1.SetGCloudHandler(dummyHandler())
	reg.Register(rc1)

	rc2 := NewRunContext("run-2")
	rc2.GCloudConfig = &GCloudConfig{Profile: "dev"}
	rc2.SetGCloudHandler(dummyHandler())
	reg.Register(rc2)

	if h := reg.FindGCloudHandler(); h == nil {
		t.Error("FindGCloudHandler returned nil for multiple runs with same profile")
	}
}

func TestRegistry_FindGCloudHandler_DifferentProfiles(t *testing.T) {
	reg := NewRegistry()

	rc1 := NewRunContext("run-1")
	rc1.GCloudConfig = &GCloudConfig{Profile: "dev"}
	rc1.SetGCloudHandler(dummyHandler())
	reg.Register(rc1)

	rc2 := NewRunContext("run-2")
	rc2.GCloudConfig = &GCloudConfig{Profile: "prod"}
	rc2.SetGCloudHandler(dummyHandler())
	reg.Register(rc2)

	if h := reg.FindGCloudHandler(); h != nil {
		t.Error("FindGCloudHandler should return nil for runs with different profiles")
	}
}

func TestRegistry_FindGCloudHandler_DefaultProfile(t *testing.T) {
	reg := NewRegistry()

	// Two runs with default (empty) profile — should succeed.
	rc1 := NewRunContext("run-1")
	rc1.GCloudConfig = &GCloudConfig{}
	rc1.SetGCloudHandler(dummyHandler())
	reg.Register(rc1)

	rc2 := NewRunContext("run-2")
	rc2.GCloudConfig = &GCloudConfig{}
	rc2.SetGCloudHandler(dummyHandler())
	reg.Register(rc2)

	if h := reg.FindGCloudHandler(); h == nil {
		t.Error("FindGCloudHandler returned nil for multiple runs with default profile")
	}
}

func TestRegistry_FindGCloudHandler_MixedGCloudAndNon(t *testing.T) {
	reg := NewRegistry()

	// One run with gcloud, one without — should return the gcloud handler.
	rc1 := NewRunContext("run-1")
	rc1.GCloudConfig = &GCloudConfig{Profile: "dev"}
	rc1.SetGCloudHandler(dummyHandler())
	reg.Register(rc1)

	rc2 := NewRunContext("run-2")
	// No gcloud config
	reg.Register(rc2)

	if h := reg.FindGCloudHandler(); h == nil {
		t.Error("FindGCloudHandler returned nil when only one run has gcloud")
	}
}

func TestRegistry_FindGCloudHandler_NoGCloud(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run-1")
	reg.Register(rc)

	if h := reg.FindGCloudHandler(); h != nil {
		t.Error("FindGCloudHandler should return nil when no runs have gcloud")
	}
}
