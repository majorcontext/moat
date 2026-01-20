package credential

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	mappings, err := s.GetSSHMappings()
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
	return os.WriteFile(s.sshPath(), data, 0600)
}

// RemoveSSHMapping removes an SSH mapping for a host.
func (s *FileStore) RemoveSSHMapping(host string) error {
	mappings, err := s.GetSSHMappings()
	if err != nil {
		return err
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
	return os.WriteFile(s.sshPath(), data, 0600)
}
