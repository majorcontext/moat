// Package storage provides run storage infrastructure for AgentOps.
// It handles persisting and loading run metadata, logs, and traces.
package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Metadata holds information about an agent run.
type Metadata struct {
	Agent     string    `json:"agent"`
	Workspace string    `json:"workspace"`
	Grants    []string  `json:"grants,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// RunStore manages storage for a single agent run.
type RunStore struct {
	dir   string
	runID string
}

// NewRunStore creates a new RunStore for the given run ID.
// It creates the run directory under baseDir if it doesn't exist.
func NewRunStore(baseDir, runID string) (*RunStore, error) {
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, err
	}
	return &RunStore{
		dir:   runDir,
		runID: runID,
	}, nil
}

// RunID returns the run identifier.
func (s *RunStore) RunID() string {
	return s.runID
}

// Dir returns the directory path for this run's storage.
func (s *RunStore) Dir() string {
	return s.dir
}

// SaveMetadata writes the metadata to metadata.json in the run directory.
func (s *RunStore) SaveMetadata(m Metadata) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "metadata.json"), data, 0644)
}

// LoadMetadata reads the metadata from metadata.json in the run directory.
func (s *RunStore) LoadMetadata() (Metadata, error) {
	var m Metadata
	data, err := os.ReadFile(filepath.Join(s.dir, "metadata.json"))
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(data, &m)
	return m, err
}

// DefaultBaseDir returns the default base directory for run storage.
// This is ~/.agentops/runs.
func DefaultBaseDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home dir cannot be determined
		return filepath.Join(".", ".agentops", "runs")
	}
	return filepath.Join(homeDir, ".agentops", "runs")
}
