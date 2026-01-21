//go:build darwin

package snapshot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// APFSBackend implements the Backend interface using APFS copy-on-write cloning.
// This uses cp -c to create instant, space-efficient directory clones on APFS.
type APFSBackend struct {
	snapshotDir string
}

// NewAPFSBackend creates a new APFS snapshot backend.
func NewAPFSBackend(snapshotDir string) *APFSBackend {
	return &APFSBackend{
		snapshotDir: snapshotDir,
	}
}

// Name returns the backend identifier.
func (b *APFSBackend) Name() string {
	return "apfs"
}

// Create creates an APFS clone of the workspace directory.
// Uses cp -c for copy-on-write cloning which is instant and space-efficient.
func (b *APFSBackend) Create(workspacePath, id string) (string, error) {
	// Validate paths don't start with "-" to prevent argument injection
	if strings.HasPrefix(filepath.Base(workspacePath), "-") {
		return "", fmt.Errorf("invalid workspace path: name cannot start with -")
	}
	if strings.HasPrefix(id, "-") {
		return "", fmt.Errorf("invalid snapshot id: cannot start with -")
	}

	// Ensure snapshot directory exists
	if err := os.MkdirAll(b.snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("create snapshot directory: %w", err)
	}

	clonePath := filepath.Join(b.snapshotDir, id)

	// Remove existing clone if present
	if _, err := os.Stat(clonePath); err == nil {
		if err := os.RemoveAll(clonePath); err != nil {
			return "", fmt.Errorf("remove existing clone: %w", err)
		}
	}

	// Use cp -c for copy-on-write cloning
	// -c: use clonefile(2) for copy-on-write
	// -R: recursive
	// -p: preserve mode, ownership, timestamps
	cmd := exec.Command("cp", "-c", "-R", "-p", "--", workspacePath, clonePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If cp -c fails (e.g., cross-device or non-APFS), fall back to regular copy
		// This shouldn't happen if IsAPFS check passed, but handle gracefully
		return "", fmt.Errorf("cp -c clone failed: %w\noutput: %s", err, string(output))
	}

	// Remove .git from the clone to save space (git history can be large)
	// The .git directory is preserved separately during restore
	// Non-fatal if removal fails - the snapshot is still valid, just larger
	gitDir := filepath.Join(clonePath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		_ = os.RemoveAll(gitDir)
	}

	return clonePath, nil
}

// Restore restores the workspace from an APFS clone (in-place).
// Preserves the .git directory in the workspace.
func (b *APFSBackend) Restore(workspacePath, nativeRef string) error {
	// Validate paths don't start with "-" to prevent argument injection
	if strings.HasPrefix(filepath.Base(workspacePath), "-") {
		return fmt.Errorf("invalid workspace path: name cannot start with -")
	}
	if strings.HasPrefix(filepath.Base(nativeRef), "-") {
		return fmt.Errorf("invalid snapshot reference: name cannot start with -")
	}

	// First, preserve the .git directory if it exists
	gitDir := filepath.Join(workspacePath, ".git")
	var gitBackup string
	if _, err := os.Stat(gitDir); err == nil {
		gitBackup = gitDir + ".backup"
		if err := os.Rename(gitDir, gitBackup); err != nil {
			return fmt.Errorf("backup .git directory: %w", err)
		}
	}

	// Clean the workspace (except .git backup)
	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		if gitBackup != "" {
			_ = os.Rename(gitBackup, gitDir)
		}
		return fmt.Errorf("read workspace directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == ".git.backup" {
			continue
		}
		path := filepath.Join(workspacePath, name)
		if removeErr := os.RemoveAll(path); removeErr != nil {
			if gitBackup != "" {
				_ = os.Rename(gitBackup, gitDir)
			}
			return fmt.Errorf("remove %s: %w", name, removeErr)
		}
	}

	// Copy files from clone to workspace using cp -c
	// We need to copy contents, not the directory itself
	cloneEntries, err := os.ReadDir(nativeRef)
	if err != nil {
		if gitBackup != "" {
			_ = os.Rename(gitBackup, gitDir)
		}
		return fmt.Errorf("read clone directory: %w", err)
	}

	for _, entry := range cloneEntries {
		src := filepath.Join(nativeRef, entry.Name())
		dst := filepath.Join(workspacePath, entry.Name())

		cmd := exec.Command("cp", "-c", "-R", "-p", "--", src, dst)
		if output, err := cmd.CombinedOutput(); err != nil {
			if gitBackup != "" {
				_ = os.Rename(gitBackup, gitDir)
			}
			return fmt.Errorf("restore %s: %w\noutput: %s", entry.Name(), err, string(output))
		}
	}

	// Restore the .git directory
	if gitBackup != "" {
		if err := os.Rename(gitBackup, gitDir); err != nil {
			return fmt.Errorf("restore .git directory: %w", err)
		}
	}

	return nil
}

// RestoreTo restores an APFS clone to a different directory.
func (b *APFSBackend) RestoreTo(nativeRef, destPath string) error {
	// Validate paths don't start with "-" to prevent argument injection
	if strings.HasPrefix(filepath.Base(nativeRef), "-") {
		return fmt.Errorf("invalid snapshot reference: name cannot start with -")
	}
	if strings.HasPrefix(filepath.Base(destPath), "-") {
		return fmt.Errorf("invalid destination path: name cannot start with -")
	}

	// Ensure destination exists
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	// Copy contents from clone to destination
	entries, err := os.ReadDir(nativeRef)
	if err != nil {
		return fmt.Errorf("read clone directory: %w", err)
	}

	for _, entry := range entries {
		src := filepath.Join(nativeRef, entry.Name())
		dst := filepath.Join(destPath, entry.Name())

		cmd := exec.Command("cp", "-c", "-R", "-p", "--", src, dst)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("copy %s: %w\noutput: %s", entry.Name(), err, string(output))
		}
	}

	return nil
}

// Delete removes an APFS clone directory.
func (b *APFSBackend) Delete(nativeRef string) error {
	if err := os.RemoveAll(nativeRef); err != nil {
		return fmt.Errorf("remove clone: %w", err)
	}
	return nil
}

// List returns all APFS clone snapshots in the snapshot directory.
func (b *APFSBackend) List(workspacePath string) ([]string, error) {
	entries, err := os.ReadDir(b.snapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot directory: %w", err)
	}

	var clones []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "snap_") {
			clones = append(clones, filepath.Join(b.snapshotDir, entry.Name()))
		}
	}

	return clones, nil
}

// IsAPFS checks if the given path is on an APFS filesystem.
func IsAPFS(path string) bool {
	// Get the absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	// Get mount point first
	mountPoint, err := getMountPoint(absPath)
	if err != nil {
		return false
	}

	// Use diskutil info to check filesystem type
	cmd := exec.Command("diskutil", "info", mountPoint)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	// Look for APFS indicators
	outputStr := string(output)
	return strings.Contains(outputStr, "APFS")
}

// getMountPoint returns the mount point for a given path.
func getMountPoint(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("get absolute path: %w", err)
	}

	// Use df to get the mount point
	cmd := exec.Command("df", absPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("df command failed: %w\noutput: %s", err, string(output))
	}

	// Parse df output - second line contains the info
	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("unexpected df output: %s", string(output))
	}

	// Mount point is the last field
	fields := strings.Fields(lines[1])
	if len(fields) < 1 {
		return "", fmt.Errorf("unexpected df output format: %s", lines[1])
	}

	return fields[len(fields)-1], nil
}

// Compile-time check that APFSBackend implements Backend.
var _ Backend = (*APFSBackend)(nil)
