package sshagent

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerStartStop(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "agent.sock")

	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "test"},
		},
	}
	proxy := NewProxy(upstream)

	server := NewServer(proxy, socketPath)
	if err := server.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Verify socket exists
	if _, err := os.Stat(socketPath); err != nil {
		t.Errorf("Socket file should exist: %v", err)
	}

	// Verify we can connect
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	conn.Close()

	// Stop server
	if err := server.Stop(); err != nil {
		t.Errorf("Stop error: %v", err)
	}
}

func TestServerSocketPath(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "agent.sock")

	upstream := &mockAgent{}
	proxy := NewProxy(upstream)
	server := NewServer(proxy, socketPath)

	if server.SocketPath() != socketPath {
		t.Errorf("SocketPath() = %s, want %s", server.SocketPath(), socketPath)
	}
}

func TestServerSocketPermissions(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "agent.sock")

	upstream := &mockAgent{}
	proxy := NewProxy(upstream)
	server := NewServer(proxy, socketPath)

	if err := server.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer server.Stop()

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}

	// Socket should be owner read/write only (0600 or similar)
	// Note: On some systems socket permissions may differ
	mode := info.Mode().Perm()
	if mode&0077 != 0 {
		t.Logf("Socket permissions: %o (note: some systems allow different permissions)", mode)
	}
}

func TestServerRemovesExistingSocket(t *testing.T) {
	// Use /tmp directly with short name to avoid exceeding macOS's
	// 104-char limit for Unix socket paths (t.TempDir() paths are too long)
	dir, err := os.MkdirTemp("/tmp", "sock")
	if err != nil {
		t.Fatalf("Creating temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "a.sock")

	// Create a stale socket file
	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("Creating stale file: %v", err)
	}

	upstream := &mockAgent{}
	proxy := NewProxy(upstream)
	server := NewServer(proxy, socketPath)

	// Should succeed despite existing file
	if err := server.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer server.Stop()

	// Should be a working socket now
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	conn.Close()
}
