package credential

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SSHMapping maps a host to an SSH key fingerprint.
type SSHMapping struct {
	Host           string    `json:"host"`
	KeyFingerprint string    `json:"key_fingerprint"`
	KeyPath        string    `json:"key_path,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// sshCredential stores all SSH host-to-key mappings.
type sshCredential struct {
	Mappings []SSHMapping `json:"mappings"`
}

func (s *FileStore) sshPath() string {
	return filepath.Join(s.dir, "ssh.json")
}

// GetSSHMappings returns all SSH host-to-key mappings.
func (s *FileStore) GetSSHMappings() ([]SSHMapping, error) {
	local, err := s.localSSHMappings()
	if err != nil {
		return nil, err
	}
	byHost := make(map[string]SSHMapping)
	if s.parent != nil {
		inherited, err := s.parent.GetSSHMappings()
		if err != nil {
			return nil, err
		}
		for _, m := range inherited {
			byHost[m.Host] = m
		}
	}
	for _, m := range local {
		byHost[m.Host] = m
	}
	var mappings []SSHMapping
	for host, m := range byHost {
		if err := validateProvider(Provider("ssh:" + host)); err != nil {
			return nil, err
		}
		blocked, err := s.isRevoked(Provider("ssh:" + host))
		if err != nil {
			return nil, err
		}
		if !blocked {
			mappings = append(mappings, m)
		}
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].Host < mappings[j].Host })
	return mappings, nil
}

func (s *FileStore) localSSHMappings() ([]SSHMapping, error) {
	data, err := os.ReadFile(s.sshPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cred sshCredential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, err
	}
	return cred.Mappings, nil
}

// GetSSHMappingsForHosts returns mappings for the specified hosts.
func (s *FileStore) GetSSHMappingsForHosts(hosts []string) ([]SSHMapping, error) {
	all, err := s.GetSSHMappings()
	if err != nil {
		return nil, err
	}

	hostSet := make(map[string]bool)
	for _, h := range hosts {
		hostSet[h] = true
	}

	var result []SSHMapping
	for _, m := range all {
		if hostSet[m.Host] {
			result = append(result, m)
		}
	}
	return result, nil
}

// AddSSHMapping adds or updates an SSH host-to-key mapping.
func (s *FileStore) AddSSHMapping(mapping SSHMapping) error {
	key := Provider("ssh:" + mapping.Host)
	if err := validateProvider(key); err != nil {
		return err
	}
	mappings, err := s.localSSHMappings()
	if err != nil {
		return err
	}

	// Update existing or append
	found := false
	for i, m := range mappings {
		if m.Host == mapping.Host {
			mapping.CreatedAt = time.Now()
			mappings[i] = mapping
			found = true
			break
		}
	}
	if !found {
		mapping.CreatedAt = time.Now()
		mappings = append(mappings, mapping)
	}

	cred := sshCredential{Mappings: mappings}
	data, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.sshPath(), data, 0o600); err != nil {
		return err
	}
	if err := os.Remove(s.revokedPath(key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveSSHMapping removes an SSH mapping for a host.
func (s *FileStore) RemoveSSHMapping(host string) error {
	key := Provider("ssh:" + host)
	if err := validateProvider(key); err != nil {
		return err
	}
	mappings, err := s.localSSHMappings()
	if err != nil {
		return err
	}
	if s.parent != nil {
		if writeErr := os.WriteFile(s.revokedPath(key), nil, 0o600); writeErr != nil {
			return writeErr
		}
	}

	var filtered []SSHMapping
	for _, m := range mappings {
		if m.Host != host {
			filtered = append(filtered, m)
		}
	}

	cred := sshCredential{Mappings: filtered}
	data, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.sshPath(), data, 0o600)
}

// SSHSource returns the profile supplying a host mapping.
func (s *FileStore) SSHSource(host string) (string, error) {
	key := Provider("ssh:" + host)
	if err := validateProvider(key); err != nil {
		return "", err
	}
	if blocked, err := s.isRevoked(key); err != nil {
		return "", err
	} else if blocked {
		return "", fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	mappings, err := s.localSSHMappings()
	if err != nil {
		return "", err
	}
	for _, m := range mappings {
		if m.Host == host {
			return s.profile, nil
		}
	}
	if s.parent != nil {
		return s.parent.SSHSource(host)
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, key)
}
