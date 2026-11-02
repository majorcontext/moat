//go:build !windows

package githubscope

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiscoverSkipsFIFOConfig(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "fifo"
		if symlink {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(ws, ".git"), 0700))
			config := filepath.Join(ws, ".git", "config")
			fifo := config
			if symlink {
				fifo = filepath.Join(t.TempDir(), "fifo")
			}
			require.NoError(t, syscall.Mkfifo(fifo, 0600))
			if symlink {
				require.NoError(t, os.Symlink(fifo, config))
			}
			done := make(chan error, 1)
			go func() { _, err := Discover(ws); done <- err }()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(time.Second):
				// Unblock a regressed implementation before cleaning up the FIFO.
				f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
				if err == nil {
					f.Close()
				}
				<-done
				t.Fatal("discovery blocked opening a FIFO")
			}
		})
	}
}
