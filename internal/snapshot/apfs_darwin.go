//go:build darwin

package snapshot

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// APFSBackend implements the Backend interface using macOS APFS snapshots via tmutil.
type APFSBackend struct{}

// NewAPFSBackend creates a new APFS snapshot backend.
func NewAPFSBackend() *APFSBackend {
	return &APFSBackend{}
}

// Name returns the backend identifier.
func (b *APFSBackend) Name() string {
	return "apfs"
}

// Create creates an APFS snapshot of the volume containing workspacePath.
// The id parameter is used as a label but APFS generates its own snapshot name.
// Returns the snapshot name as the native reference.
func (b *APFSBackend) Create(workspacePath, id string) (string, error) {
	// Get the mount point for the workspace path
	mountPoint, err := getMountPoint(workspacePath)
	if err != nil {
		return "", fmt.Errorf("get mount point: %w", err)
	}

	// Create a local snapshot using tmutil
	// tmutil localsnapshot <mount_point>
	cmd := exec.Command("tmutil", "localsnapshot", mountPoint)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmutil localsnapshot failed: %w\noutput: %s", err, string(output))
	}

	// Parse the snapshot name from output
	// Output format: "Created local snapshot with date: 2024-01-15-123456"
	outputStr := strings.TrimSpace(string(output))
	const prefix = "Created local snapshot with date: "
	if !strings.HasPrefix(outputStr, prefix) {
		return "", fmt.Errorf("unexpected tmutil output: %s", outputStr)
	}

	snapshotDate := strings.TrimPrefix(outputStr, prefix)
	// The snapshot name format is: com.apple.TimeMachine.YYYY-MM-DD-HHMMSS.local
	snapshotName := fmt.Sprintf("com.apple.TimeMachine.%s.local", snapshotDate)

	return snapshotName, nil
}

// Restore restores an APFS snapshot to the workspace (in-place).
// Note: APFS snapshot restore typically requires elevated privileges and
// may not support in-place restore to a subdirectory.
func (b *APFSBackend) Restore(workspacePath, nativeRef string) error {
	// Get the mount point for the workspace
	mountPoint, err := getMountPoint(workspacePath)
	if err != nil {
		return fmt.Errorf("get mount point: %w", err)
	}

	// tmutil restore requires the snapshot path and destination
	// Format: /Volumes/<volume>/.timemachine/<snapshot>
	snapshotPath := filepath.Join(mountPoint, ".timemachine", nativeRef)

	// Use tmutil restore to restore the snapshot
	// Note: This restores the entire volume state, which may not be what we want
	// for a workspace subdirectory. For workspace-level restore, consider using
	// the archive backend instead.
	cmd := exec.Command("tmutil", "restore", snapshotPath, workspacePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmutil restore failed: %w\noutput: %s", err, string(output))
	}

	return nil
}

// RestoreTo restores an APFS snapshot to a different directory.
func (b *APFSBackend) RestoreTo(nativeRef, destPath string) error {
	// For RestoreTo, we need to know the source volume
	// Since nativeRef is just the snapshot name, we need to find it
	// This is a limitation - we'd need to store the mount point in the metadata

	// Try the root volume as default
	snapshotPath := filepath.Join("/", ".timemachine", nativeRef)

	cmd := exec.Command("tmutil", "restore", snapshotPath, destPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmutil restore failed: %w\noutput: %s", err, string(output))
	}

	return nil
}

// Delete removes an APFS snapshot.
func (b *APFSBackend) Delete(nativeRef string) error {
	// Extract the date portion from the snapshot name
	// Format: com.apple.TimeMachine.YYYY-MM-DD-HHMMSS.local
	datePart := extractDateFromSnapshotName(nativeRef)
	if datePart == "" {
		// If we can't extract the date, try using the full name
		datePart = nativeRef
	}

	// tmutil deletelocalsnapshots <date>
	// The date format should be: YYYY-MM-DD-HHMMSS
	cmd := exec.Command("tmutil", "deletelocalsnapshots", datePart)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmutil deletelocalsnapshots failed: %w\noutput: %s", err, string(output))
	}

	return nil
}

// List returns all APFS snapshots for the volume containing workspacePath.
func (b *APFSBackend) List(workspacePath string) ([]string, error) {
	// Get the mount point for the workspace path
	mountPoint, err := getMountPoint(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("get mount point: %w", err)
	}

	// tmutil listlocalsnapshots <mount_point>
	cmd := exec.Command("tmutil", "listlocalsnapshots", mountPoint)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmutil listlocalsnapshots failed: %w\noutput: %s", err, string(output))
	}

	// Parse the output - one snapshot per line
	// Format: com.apple.TimeMachine.YYYY-MM-DD-HHMMSS.local
	var snapshots []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && strings.HasPrefix(line, "com.apple.TimeMachine.") {
			snapshots = append(snapshots, line)
		}
	}

	return snapshots, scanner.Err()
}

// IsAPFS checks if the given path is on an APFS filesystem.
func IsAPFS(path string) bool {
	// Get the absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	// Use diskutil info to get filesystem information
	cmd := exec.Command("diskutil", "info", absPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If diskutil fails, try getting the mount point first
		mountPoint, err := getMountPoint(absPath)
		if err != nil {
			return false
		}
		cmd = exec.Command("diskutil", "info", mountPoint)
		output, err = cmd.CombinedOutput()
		if err != nil {
			return false
		}
	}

	// Look for "Type (Bundle): apfs" or "File System Personality: APFS"
	outputStr := string(output)
	return strings.Contains(outputStr, "Type (Bundle):  apfs") ||
		strings.Contains(outputStr, "File System Personality:  APFS") ||
		strings.Contains(outputStr, "apfs")
}

// getMountPoint returns the mount point for a given path.
func getMountPoint(path string) (string, error) {
	// Get the absolute path
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
	// Format: Filesystem 512-blocks Used Available Capacity iused ifree %iused Mounted on
	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("unexpected df output: %s", string(output))
	}

	// The mount point is the last field
	fields := strings.Fields(lines[1])
	if len(fields) < 1 {
		return "", fmt.Errorf("unexpected df output format: %s", lines[1])
	}

	// Mount point is the last field
	mountPoint := fields[len(fields)-1]
	return mountPoint, nil
}

// extractDateFromSnapshotName extracts the date portion from an APFS snapshot name.
// Input format: com.apple.TimeMachine.YYYY-MM-DD-HHMMSS.local
// Output format: YYYY-MM-DD-HHMMSS
func extractDateFromSnapshotName(name string) string {
	const prefix = "com.apple.TimeMachine."
	const suffix = ".local"

	if !strings.HasPrefix(name, prefix) {
		return ""
	}

	name = strings.TrimPrefix(name, prefix)
	name = strings.TrimSuffix(name, suffix)

	return name
}

// Compile-time check that APFSBackend implements Backend.
var _ Backend = (*APFSBackend)(nil)
