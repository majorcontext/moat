package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManager_Create(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	session, err := mgr.Create("/workspace", "run-123", "test-session", []string{"github"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if session.ID != "run-123" {
		t.Errorf("ID = %q, want %q", session.ID, "run-123")
	}
	if session.Name != "test-session" {
		t.Errorf("Name = %q, want %q", session.Name, "test-session")
	}
	if session.Workspace != "/workspace" {
		t.Errorf("Workspace = %q, want %q", session.Workspace, "/workspace")
	}
	if session.RunID != "run-123" {
		t.Errorf("RunID = %q, want %q", session.RunID, "run-123")
	}
	if len(session.Grants) != 1 || session.Grants[0] != "github" {
		t.Errorf("Grants = %v, want [github]", session.Grants)
	}
	if session.State != StateRunning {
		t.Errorf("State = %q, want %q", session.State, StateRunning)
	}
	if session.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if session.LastAccessedAt.IsZero() {
		t.Error("LastAccessedAt should not be zero")
	}

	// Verify file was created
	metadataPath := filepath.Join(dir, "run-123", "metadata.json")
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		t.Error("metadata.json file was not created")
	}
}

func TestManager_Get(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Create a session
	created, err := mgr.Create("/workspace", "run-456", "my-session", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Test get by ID
	t.Run("get by ID", func(t *testing.T) {
		got, err := mgr.Get("run-456")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("Get() ID = %q, want %q", got.ID, created.ID)
		}
	})

	// Test get by name
	t.Run("get by name", func(t *testing.T) {
		got, err := mgr.Get("my-session")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got.Name != created.Name {
			t.Errorf("Get() Name = %q, want %q", got.Name, created.Name)
		}
	})

	// Test get non-existent
	t.Run("get non-existent", func(t *testing.T) {
		_, err := mgr.Get("does-not-exist")
		if err == nil {
			t.Error("Get() expected error for non-existent session, got nil")
		}
	})
}

func TestManager_GetByWorkspace(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Create sessions with different workspaces
	_, err := mgr.Create("/workspace1", "run-1", "session-1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Create a second session for the same workspace (after a delay)
	time.Sleep(10 * time.Millisecond)
	session2, err := mgr.Create("/workspace1", "run-2", "session-2", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = mgr.Create("/workspace2", "run-3", "session-3", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Should return the most recent session for workspace1
	got, err := mgr.GetByWorkspace("/workspace1")
	if err != nil {
		t.Fatalf("GetByWorkspace() error = %v", err)
	}
	if got.ID != session2.ID {
		t.Errorf("GetByWorkspace() ID = %q, want %q (most recent)", got.ID, session2.ID)
	}

	// Test non-existent workspace
	_, err = mgr.GetByWorkspace("/workspace-does-not-exist")
	if err == nil {
		t.Error("GetByWorkspace() expected error for non-existent workspace, got nil")
	}
}

func TestManager_List(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Empty list initially
	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("List() returned %d sessions, want 0", len(sessions))
	}

	// Create sessions
	_, _ = mgr.Create("/ws1", "run-a", "session-a", nil)
	time.Sleep(10 * time.Millisecond)
	_, _ = mgr.Create("/ws2", "run-b", "session-b", nil)
	time.Sleep(10 * time.Millisecond)
	_, _ = mgr.Create("/ws3", "run-c", "session-c", nil)

	sessions, err = mgr.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("List() returned %d sessions, want 3", len(sessions))
	}

	// Verify sorted by LastAccessedAt (most recent first)
	for i := 0; i < len(sessions)-1; i++ {
		if sessions[i].LastAccessedAt.Before(sessions[i+1].LastAccessedAt) {
			t.Errorf("List() not sorted: session[%d].LastAccessedAt < session[%d].LastAccessedAt", i, i+1)
		}
	}
}

func TestManager_UpdateState(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	_, err := mgr.Create("/workspace", "run-state", "session-state", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = mgr.UpdateState("run-state", StateStopped)
	if err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}

	got, _ := mgr.Get("run-state")
	if got.State != StateStopped {
		t.Errorf("State = %q, want %q", got.State, StateStopped)
	}
}

func TestManager_Touch(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	created, err := mgr.Create("/workspace", "run-touch", "session-touch", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	originalTime := created.LastAccessedAt

	time.Sleep(10 * time.Millisecond)
	err = mgr.Touch("run-touch")
	if err != nil {
		t.Fatalf("Touch() error = %v", err)
	}

	got, _ := mgr.Get("run-touch")
	if !got.LastAccessedAt.After(originalTime) {
		t.Error("Touch() should update LastAccessedAt")
	}
}

func TestManager_Delete(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	_, err := mgr.Create("/workspace", "run-delete", "session-delete", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = mgr.Delete("run-delete")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify session is gone
	_, err = mgr.Get("run-delete")
	if err == nil {
		t.Error("Get() expected error after delete, got nil")
	}

	// Verify directory is gone
	sessionDir := filepath.Join(dir, "run-delete")
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Error("Session directory should be deleted")
	}
}

func TestManager_Delete_InvalidID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Test invalid session IDs (path traversal attempts)
	invalidIDs := []string{
		"../etc/passwd",
		"./secret",
		"",
		"session with spaces",
	}

	for _, id := range invalidIDs {
		err := mgr.Delete(id)
		if err == nil {
			t.Errorf("Delete(%q) expected error for invalid ID, got nil", id)
		}
	}
}

func TestManager_CleanupOldSessions(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Create an old stopped session
	oldSession, _ := mgr.Create("/ws1", "run-old", "old-session", nil)
	oldSession.LastAccessedAt = time.Now().Add(-48 * time.Hour)
	oldSession.State = StateStopped
	_ = mgr.save(oldSession)

	// Create a recent stopped session
	_, _ = mgr.Create("/ws2", "run-recent", "recent-session", nil)
	_ = mgr.UpdateState("run-recent", StateStopped)

	// Create an old running session (should not be deleted)
	runningSession, _ := mgr.Create("/ws3", "run-running", "running-session", nil)
	runningSession.LastAccessedAt = time.Now().Add(-48 * time.Hour)
	_ = mgr.save(runningSession)

	// Cleanup sessions older than 24 hours
	err := mgr.CleanupOldSessions(24 * time.Hour)
	if err != nil {
		t.Fatalf("CleanupOldSessions() error = %v", err)
	}

	sessions, _ := mgr.List()
	if len(sessions) != 2 {
		t.Errorf("List() after cleanup returned %d sessions, want 2", len(sessions))
	}

	// Old stopped session should be gone
	_, err = mgr.Get("run-old")
	if err == nil {
		t.Error("Old stopped session should be deleted")
	}

	// Recent session and running session should remain
	if _, err := mgr.Get("run-recent"); err != nil {
		t.Error("Recent session should not be deleted")
	}
	if _, err := mgr.Get("run-running"); err != nil {
		t.Error("Running session should not be deleted even if old")
	}
}

func TestManager_Concurrency(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Create initial session
	_, err := mgr.Create("/workspace", "run-concurrent", "concurrent-session", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Concurrent reads and writes
	var wg sync.WaitGroup
	errors := make(chan error, 20)

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.Get("run-concurrent")
			if err != nil {
				errors <- err
			}
		}()
	}

	// Concurrent writers (Touch updates LastAccessedAt)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := mgr.Touch("run-concurrent")
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent operation error: %v", err)
	}
}

func TestValidSessionID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"run-123", true},
		{"abc123", true},
		{"session-name", true},
		{"A1B2C3", true},
		{"a", true},                  // single character is valid
		{"a1", true},                 // two characters
		{"", false},                  // empty
		{"-starts-with-dash", false}, // starts with dash
		{"ends-with-dash-", false},   // ends with dash
		{"has spaces", false},
		{"has/slash", false},
		{"../traversal", false},
		{"has.period", false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := validSessionID.MatchString(tt.id)
			if got != tt.valid {
				t.Errorf("validSessionID.MatchString(%q) = %v, want %v", tt.id, got, tt.valid)
			}
		})
	}
}

func TestSession_JSON(t *testing.T) {
	session := &Session{
		ID:             "run-json",
		Name:           "json-session",
		Workspace:      "/workspace/path",
		RunID:          "run-json",
		Grants:         []string{"github", "openai"},
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		State:          StateRunning,
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	// Unmarshal back
	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if got.ID != session.ID {
		t.Errorf("ID = %q, want %q", got.ID, session.ID)
	}
	if got.Name != session.Name {
		t.Errorf("Name = %q, want %q", got.Name, session.Name)
	}
	if got.Workspace != session.Workspace {
		t.Errorf("Workspace = %q, want %q", got.Workspace, session.Workspace)
	}
	if len(got.Grants) != len(session.Grants) {
		t.Errorf("Grants length = %d, want %d", len(got.Grants), len(session.Grants))
	}
	if got.State != session.State {
		t.Errorf("State = %q, want %q", got.State, session.State)
	}
}

func TestManager_Dir(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if mgr.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", mgr.Dir(), dir)
	}
}

func TestManager_Create_InvalidID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Test creating with invalid session IDs
	invalidIDs := []string{
		"../traversal",
		"has/slash",
		"-starts-with-dash",
	}

	for _, id := range invalidIDs {
		_, err := mgr.Create("/workspace", id, "name", nil)
		if err == nil {
			t.Errorf("Create() with ID %q expected error, got nil", id)
		}
	}
}

func TestManager_load_InvalidID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	_, err := mgr.load("../etc/passwd")
	if err == nil {
		t.Error("load() with path traversal ID expected error, got nil")
	}
}

func TestManager_CorruptedMetadata(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Create a session directory with corrupted metadata
	sessionDir := filepath.Join(dir, "corrupted-session")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session directory: %v", err)
	}
	metadataPath := filepath.Join(sessionDir, "metadata.json")
	if err := os.WriteFile(metadataPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("Failed to write corrupted metadata: %v", err)
	}

	// List should skip corrupted sessions
	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("List() returned %d sessions, want 0 (corrupted should be skipped)", len(sessions))
	}
}
