//go:build !darwin

package snapshot

import "fmt"

// ErrAPFSNotAvailable is returned when APFS operations are attempted on non-Darwin platforms.
var ErrAPFSNotAvailable = fmt.Errorf("APFS snapshots are only available on macOS")

// APFSBackend is a stub for non-darwin platforms.
// On non-darwin systems, all methods return errors to indicate APFS is unavailable.
type APFSBackend struct {
	snapshotDir string
}

// NewAPFSBackend returns a stub APFSBackend on non-darwin platforms.
// The snapshotDir parameter is accepted for API compatibility but not used.
func NewAPFSBackend(snapshotDir string) *APFSBackend {
	return &APFSBackend{
		snapshotDir: snapshotDir,
	}
}

// Name returns the backend identifier.
func (b *APFSBackend) Name() string {
	return "apfs"
}

// Create returns an error on non-darwin platforms.
func (b *APFSBackend) Create(workspacePath, id string) (string, error) {
	return "", ErrAPFSNotAvailable
}

// Restore returns an error on non-darwin platforms.
func (b *APFSBackend) Restore(workspacePath, nativeRef string) error {
	return ErrAPFSNotAvailable
}

// RestoreTo returns an error on non-darwin platforms.
func (b *APFSBackend) RestoreTo(nativeRef, destPath string) error {
	return ErrAPFSNotAvailable
}

// Delete returns an error on non-darwin platforms.
func (b *APFSBackend) Delete(nativeRef string) error {
	return ErrAPFSNotAvailable
}

// List returns an error on non-darwin platforms.
func (b *APFSBackend) List(workspacePath string) ([]string, error) {
	return nil, ErrAPFSNotAvailable
}

// IsAPFS returns false on non-darwin platforms.
func IsAPFS(path string) bool {
	return false
}

// Compile-time check that APFSBackend implements Backend.
var _ Backend = (*APFSBackend)(nil)
