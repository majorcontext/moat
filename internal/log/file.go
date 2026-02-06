package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileWriter manages daily log file rotation and symlink updates.
type FileWriter struct {
	dir      string
	mu       sync.Mutex
	file     *os.File
	currDate string
}

// NewFileWriter creates a FileWriter that writes to dir/YYYY-MM-DD.jsonl.
func NewFileWriter(dir string) (*FileWriter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating debug log dir: %w", err)
	}

	fw := &FileWriter{dir: dir}
	if err := fw.rotate(); err != nil {
		return nil, err
	}
	return fw, nil
}

// Write implements io.Writer. It handles daily rotation.
func (fw *FileWriter) Write(p []byte) (n int, err error) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if today != fw.currDate {
		if err := fw.rotateLocked(); err != nil {
			return 0, err
		}
	}

	return fw.file.Write(p)
}

// Close closes the underlying file.
func (fw *FileWriter) Close() error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.file != nil {
		return fw.file.Close()
	}
	return nil
}

func (fw *FileWriter) rotate() error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.rotateLocked()
}

func (fw *FileWriter) rotateLocked() error {
	if fw.file != nil {
		fw.file.Close()
	}

	today := time.Now().Format("2006-01-02")
	filename := today + ".jsonl"
	path := filepath.Join(fw.dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}

	fw.file = f
	fw.currDate = today

	// Update symlink atomically
	fw.updateSymlink(filename)

	return nil
}

func (fw *FileWriter) updateSymlink(target string) {
	symlinkPath := filepath.Join(fw.dir, "latest")
	tmpPath := symlinkPath + ".tmp"

	// Remove temp if exists, create new symlink, rename
	os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return // Best effort
	}
	_ = os.Rename(tmpPath, symlinkPath) // Best effort
}
