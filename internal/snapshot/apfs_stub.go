//go:build !darwin

package snapshot

// APFSBackend is a stub for non-darwin platforms.
// On non-darwin systems, all methods return nil/empty values.
type APFSBackend struct{}

// NewAPFSBackend returns a stub APFSBackend on non-darwin platforms.
func NewAPFSBackend() *APFSBackend {
	return &APFSBackend{}
}

// Name returns the backend identifier.
func (b *APFSBackend) Name() string {
	return "apfs"
}

// Create is a no-op on non-darwin platforms.
func (b *APFSBackend) Create(workspacePath, id string) (string, error) {
	return "", nil
}

// Restore is a no-op on non-darwin platforms.
func (b *APFSBackend) Restore(workspacePath, nativeRef string) error {
	return nil
}

// RestoreTo is a no-op on non-darwin platforms.
func (b *APFSBackend) RestoreTo(nativeRef, destPath string) error {
	return nil
}

// Delete is a no-op on non-darwin platforms.
func (b *APFSBackend) Delete(nativeRef string) error {
	return nil
}

// List returns an empty slice on non-darwin platforms.
func (b *APFSBackend) List(workspacePath string) ([]string, error) {
	return nil, nil
}

// IsAPFS returns false on non-darwin platforms.
func IsAPFS(path string) bool {
	return false
}

// Compile-time check that APFSBackend implements Backend.
var _ Backend = (*APFSBackend)(nil)
