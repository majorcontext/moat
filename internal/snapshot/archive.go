package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// ArchiveOptions configures the archive backend behavior.
type ArchiveOptions struct {
	// UseGitignore enables parsing .gitignore files to exclude matching paths.
	UseGitignore bool
	// Additional specifies extra patterns to exclude (gitignore syntax).
	Additional []string
}

// ArchiveBackend implements the Backend interface using tar.gz archives.
type ArchiveBackend struct {
	snapshotDir string
	opts        ArchiveOptions
}

// NewArchiveBackend creates a new archive-based snapshot backend.
func NewArchiveBackend(snapshotDir string, opts ArchiveOptions) *ArchiveBackend {
	return &ArchiveBackend{
		snapshotDir: snapshotDir,
		opts:        opts,
	}
}

// Name returns the backend identifier.
func (b *ArchiveBackend) Name() string {
	return "archive"
}

// Create creates a tar.gz archive of the workspace.
func (b *ArchiveBackend) Create(workspacePath, id string) (string, error) {
	// Ensure snapshot directory exists
	if err := os.MkdirAll(b.snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("create snapshot directory: %w", err)
	}

	archivePath := filepath.Join(b.snapshotDir, id+".tar.gz")

	// Create the archive file
	file, err := os.Create(archivePath)
	if err != nil {
		return "", fmt.Errorf("create archive file: %w", err)
	}
	defer file.Close()

	gw := gzip.NewWriter(file)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Build the ignore matcher
	matcher, err := b.buildMatcher(workspacePath)
	if err != nil {
		return "", fmt.Errorf("build ignore matcher: %w", err)
	}

	// Walk the workspace and add files to the archive
	err = filepath.WalkDir(workspacePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relPath == "." {
			return nil
		}

		// Always skip .git directory
		if relPath == ".git" || strings.HasPrefix(relPath, ".git/") || strings.HasPrefix(relPath, ".git"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if path should be ignored
		if b.shouldIgnore(matcher, relPath, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("get file info for %s: %w", relPath, err)
		}

		// Handle symlinks
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read symlink %s: %w", relPath, err)
			}
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return fmt.Errorf("create tar header for %s: %w", relPath, err)
		}
		header.Name = relPath

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header for %s: %w", relPath, err)
		}

		// Write file content for regular files
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open file %s: %w", relPath, err)
			}
			_, copyErr := io.Copy(tw, f)
			f.Close() // Close immediately, not deferred, to avoid accumulating file handles
			if copyErr != nil {
				return fmt.Errorf("copy file content %s: %w", relPath, copyErr)
			}
		}

		return nil
	})

	if err != nil {
		// Clean up on error
		os.Remove(archivePath)
		return "", fmt.Errorf("walk workspace: %w", err)
	}

	return archivePath, nil
}

// Restore extracts the archive to the workspace, preserving .git directory.
func (b *ArchiveBackend) Restore(workspacePath, nativeRef string) error {
	// First, preserve the .git directory if it exists
	gitDir := filepath.Join(workspacePath, ".git")
	var gitBackup string
	if _, err := os.Stat(gitDir); err == nil {
		// Create a temporary backup of .git
		gitBackup = gitDir + ".backup"
		if err := os.Rename(gitDir, gitBackup); err != nil {
			return fmt.Errorf("backup .git directory: %w", err)
		}
	}

	// Clean the workspace (except .git backup)
	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		// Restore .git on error (best effort)
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
		if err := os.RemoveAll(path); err != nil {
			// Restore .git on error (best effort)
			if gitBackup != "" {
				_ = os.Rename(gitBackup, gitDir)
			}
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}

	// Extract the archive
	if err := b.RestoreTo(nativeRef, workspacePath); err != nil {
		// Restore .git on error (best effort)
		if gitBackup != "" {
			_ = os.Rename(gitBackup, gitDir)
		}
		return fmt.Errorf("extract archive: %w", err)
	}

	// Restore the .git directory
	if gitBackup != "" {
		if err := os.Rename(gitBackup, gitDir); err != nil {
			return fmt.Errorf("restore .git directory: %w", err)
		}
	}

	return nil
}

// Archive extraction limits to prevent zip bomb attacks.
const (
	maxArchiveFiles     = 100000   // Maximum number of files
	maxArchiveFileSize  = 1 << 30  // 1GB per file
	maxArchiveTotalSize = 10 << 30 // 10GB total extracted size
)

// RestoreTo extracts the archive to an arbitrary destination path.
func (b *ArchiveBackend) RestoreTo(nativeRef, destPath string) error {
	file, err := os.Open(nativeRef)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	gr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	fileCount := 0
	var totalWritten int64

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}

		// Check file count limit to prevent zip bomb attacks
		fileCount++
		if fileCount > maxArchiveFiles {
			return fmt.Errorf("archive contains too many files (limit: %d)", maxArchiveFiles)
		}

		// targetPath is validated below before any filesystem operations
		targetPath := filepath.Join(destPath, header.Name) //nolint:gosec // G305: validated below

		// Ensure the target path is within destPath (prevent path traversal)
		// Use filepath.Rel to check - if it starts with ".." it escapes destPath
		relToDestPath, err := filepath.Rel(destPath, targetPath)
		if err != nil || strings.HasPrefix(relToDestPath, "..") {
			return fmt.Errorf("invalid path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			//nolint:gosec // G115: Mode is masked to permission bits which fit in uint32
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode&0777)); err != nil {
				return fmt.Errorf("create directory %s: %w", header.Name, err)
			}
		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("create parent directory for %s: %w", header.Name, err)
			}

			//nolint:gosec // G115: Mode is masked to permission bits which fit in uint32
			f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode&0777))
			if err != nil {
				return fmt.Errorf("create file %s: %w", header.Name, err)
			}

			// Check total size limit before writing
			if totalWritten > maxArchiveTotalSize {
				_ = f.Close()
				return fmt.Errorf("archive exceeds maximum total extracted size (limit: %d bytes)", maxArchiveTotalSize)
			}

			// Limit copy size to prevent decompression bombs
			written, copyErr := io.Copy(f, io.LimitReader(tr, maxArchiveFileSize))
			totalWritten += written
			if copyErr != nil {
				_ = f.Close() // Best effort close; preserve the write error
				return fmt.Errorf("write file %s: %w", header.Name, copyErr)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close file %s: %w", header.Name, err)
			}
		case tar.TypeSymlink:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("create parent directory for symlink %s: %w", header.Name, err)
			}

			// Validate symlink target doesn't escape destPath (path traversal prevention)
			// Reject absolute symlink targets - they could point anywhere on the filesystem
			if filepath.IsAbs(header.Linkname) {
				return fmt.Errorf("invalid symlink in archive: absolute path not allowed: %s -> %s", header.Name, header.Linkname)
			}

			// Resolve the symlink target relative to its location within destPath
			// This handles cases like "../sibling" which is valid within the archive
			symlinkDir := filepath.Dir(targetPath)
			resolvedTarget := filepath.Join(symlinkDir, header.Linkname) //nolint:gosec // G305: validated below
			resolvedTarget = filepath.Clean(resolvedTarget)

			// Verify the resolved target stays within destPath
			relToDestPath, err := filepath.Rel(destPath, resolvedTarget)
			if err != nil || strings.HasPrefix(relToDestPath, "..") {
				return fmt.Errorf("invalid symlink in archive: target escapes destination: %s -> %s", header.Name, header.Linkname)
			}

			// Remove existing file/symlink if present
			os.Remove(targetPath)

			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				return fmt.Errorf("create symlink %s: %w", header.Name, err)
			}
		default:
			// Skip unsupported types
			continue
		}
	}

	return nil
}

// Delete removes the archive file.
func (b *ArchiveBackend) Delete(nativeRef string) error {
	if err := os.Remove(nativeRef); err != nil {
		return fmt.Errorf("remove archive: %w", err)
	}
	return nil
}

// List returns all archive files in the snapshot directory.
// The workspacePath parameter is unused but required by the Backend interface.
func (b *ArchiveBackend) List(_ string) ([]string, error) {
	entries, err := os.ReadDir(b.snapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot directory: %w", err)
	}

	var refs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".tar.gz") {
			refs = append(refs, filepath.Join(b.snapshotDir, entry.Name()))
		}
	}

	return refs, nil
}

// buildMatcher creates a gitignore matcher from .gitignore files and additional patterns.
func (b *ArchiveBackend) buildMatcher(workspacePath string) (gitignore.Matcher, error) {
	patterns := make([]gitignore.Pattern, 0, len(b.opts.Additional)+16)

	// Add patterns from .gitignore files if enabled
	if b.opts.UseGitignore {
		err := filepath.WalkDir(workspacePath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			// Skip .git directory
			if d.IsDir() && d.Name() == ".git" {
				return filepath.SkipDir
			}

			if d.Name() == ".gitignore" {
				relDir, err := filepath.Rel(workspacePath, filepath.Dir(path))
				if err != nil {
					return err
				}

				content, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("read .gitignore at %s: %w", path, err)
				}

				// Parse patterns from this .gitignore file
				var domain []string
				if relDir != "." {
					domain = strings.Split(relDir, string(filepath.Separator))
				}

				for _, line := range strings.Split(string(content), "\n") {
					line = strings.TrimSpace(line)
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					patterns = append(patterns, gitignore.ParsePattern(line, domain))
				}
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Add additional patterns
	for _, pattern := range b.opts.Additional {
		patterns = append(patterns, gitignore.ParsePattern(pattern, nil))
	}

	return gitignore.NewMatcher(patterns), nil
}

// shouldIgnore checks if a path should be ignored based on the matcher.
func (b *ArchiveBackend) shouldIgnore(matcher gitignore.Matcher, relPath string, isDir bool) bool {
	// Convert path to components for the matcher
	pathParts := strings.Split(relPath, string(filepath.Separator))
	return matcher.Match(pathParts, isDir)
}

// Compile-time check that ArchiveBackend implements Backend.
var _ Backend = (*ArchiveBackend)(nil)
