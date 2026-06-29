package run

import (
	"errors"
	"testing"
)

// sshTestOpener and emptyTestStore are defined in manager_ssh_test.go (same
// package) and reused here.

func TestSetupProviderMounts_NoContainerHome(t *testing.T) {
	m := &Manager{}
	mounts, initFiles := m.setupProviderMounts(&Run{}, []string{"github"}, "", sshTestOpener(nil, nil))
	if mounts != nil || len(initFiles) != 0 {
		t.Fatalf("expected empty result with no container home, got mounts=%v initFiles=%v", mounts, initFiles)
	}
}

func TestSetupProviderMounts_StoreError(t *testing.T) {
	m := &Manager{}
	mounts, initFiles := m.setupProviderMounts(&Run{}, []string{"github"}, "/home/moatuser", sshTestOpener(nil, errors.New("store open failed")))
	if mounts != nil || len(initFiles) != 0 {
		t.Fatalf("expected empty result on store error, got mounts=%v initFiles=%v", mounts, initFiles)
	}
}

func TestSetupProviderMounts_CredMissSkipped(t *testing.T) {
	// An empty store has no credential for the grant, so the grant is skipped
	// (degrade, not fail) and nothing is mounted or recorded for cleanup.
	m := &Manager{}
	r := &Run{}
	mounts, initFiles := m.setupProviderMounts(r, []string{"github"}, "/home/moatuser", sshTestOpener(emptyTestStore(t), nil))
	if mounts != nil || len(initFiles) != 0 {
		t.Fatalf("expected grant with missing credential to be skipped, got mounts=%v initFiles=%v", mounts, initFiles)
	}
	if r.ProviderCleanupPaths != nil {
		t.Fatalf("expected no cleanup paths, got %v", r.ProviderCleanupPaths)
	}
}
