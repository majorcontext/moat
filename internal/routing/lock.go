package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ProxyLockInfo holds information about a running proxy.
type ProxyLockInfo struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
}

// IsAlive checks if the process is still running.
func (p *ProxyLockInfo) IsAlive() bool {
	process, err := os.FindProcess(p.PID)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Send signal 0 to check if alive.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// LoadProxyLock loads the proxy lock file from the given directory.
// Returns nil, nil if the lock file doesn't exist.
func LoadProxyLock(dir string) (*ProxyLockInfo, error) {
	path := filepath.Join(dir, "proxy.lock")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var info ProxyLockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SaveProxyLock writes the proxy lock file.
func SaveProxyLock(dir string, info ProxyLockInfo) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if info.StartedAt.IsZero() {
		info.StartedAt = time.Now()
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "proxy.lock")
	return os.WriteFile(path, data, 0644)
}

// RemoveProxyLock removes the proxy lock file.
func RemoveProxyLock(dir string) error {
	path := filepath.Join(dir, "proxy.lock")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
