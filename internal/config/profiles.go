package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// ProfileExists includes profiles created implicitly by granting credentials.
func (c *GlobalConfig) ProfileExists(name string) (bool, error) {
	if err := ValidateProfileName(name); err != nil {
		return false, err
	}
	if c != nil {
		if _, ok := c.Profiles[name]; ok {
			return true, nil
		}
	}
	info, err := os.Stat(filepath.Join(GlobalConfigDir(), "credentials", "profiles", name))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// ProfileChain returns the lookup order, including the requested profile.
// The empty string denotes the default store, which has no parent or env.
func (c *GlobalConfig) ProfileChain(name string) ([]string, error) {
	var chain []string
	seen := make(map[string]bool)
	for {
		if name == "" {
			return append(chain, ""), nil
		}
		if err := ValidateProfileName(name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("profile inheritance cycle at %q", name)
		}
		seen[name] = true
		chain = append(chain, name)
		var prof ProfileConfig
		if c != nil {
			prof = c.Profiles[name]
		}
		if prof.Inherit != "" && prof.InheritDefault {
			return nil, fmt.Errorf("profiles.%s: inherit and inherit_default are mutually exclusive", name)
		}
		if prof.InheritDefault {
			return append(chain, ""), nil
		}
		if prof.Inherit == "" {
			return chain, nil
		}
		exists, err := c.ProfileExists(prof.Inherit)
		if err != nil {
			return nil, fmt.Errorf("profiles.%s.inherit: %w", name, err)
		}
		if !exists {
			return nil, fmt.Errorf("profiles.%s: parent profile %q does not exist", name, prof.Inherit)
		}
		name = prof.Inherit
	}
}

// CreateProfile adds a profile without discarding comments or unrelated settings.
// It reads the YAML directly so malformed files are never replaced with defaults.
func CreateProfile(name string, prof ProfileConfig) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(GlobalConfigDir(), 0o700); err != nil {
		return err
	}
	// Hold a separate, stable lock file across the entire read/modify/rename.
	// Never remove it: locking the replaced config inode would lose exclusion.
	lock := flock.New(filepath.Join(GlobalConfigDir(), "config.lock"), flock.SetPermissions(0o600))
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("locking global config: %w", err)
	}
	defer func() { _ = lock.Close() }()
	path := filepath.Join(GlobalConfigDir(), "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var doc yaml.Node
	if parseErr := yaml.Unmarshal(data, &doc); parseErr != nil {
		return fmt.Errorf("reading global config: %w", parseErr)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("global config must be a mapping")
	}
	var cfg GlobalConfig
	if decodeErr := root.Decode(&cfg); decodeErr != nil {
		return decodeErr
	}
	exists, err := cfg.ProfileExists(name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("profile %q already exists", name)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]ProfileConfig)
	}
	cfg.Profiles[name] = prof
	for n := range cfg.Profiles {
		if _, chainErr := cfg.ProfileChain(n); chainErr != nil {
			return chainErr
		}
	}
	var profiles *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "profiles" {
			profiles = root.Content[i+1]
			break
		}
	}
	if profiles == nil {
		profiles = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "profiles"}, profiles)
	}
	if profiles.Tag == "!!null" {
		*profiles = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if profiles.Kind != yaml.MappingNode {
		return fmt.Errorf("profiles must be a mapping")
	}
	var value yaml.Node
	if encodeErr := value.Encode(prof); encodeErr != nil {
		return encodeErr
	}
	profiles.Content = append(profiles.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, &value)
	return writeProfileConfig(&doc)
}

func writeProfileConfig(doc *yaml.Node) error {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(GlobalConfigDir(), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(GlobalConfigDir(), "config.yaml"))
}

// RemoveProfile removes a named profile's settings and local credential store.
// A profile with children cannot be removed without breaking their lookup chain.
func RemoveProfile(name string) error { return clearProfile(name, true) }

// ResetProfile clears local credentials, SSH mappings and exclusions while
// preserving profile settings. Inherited credentials remain available.
func ResetProfile(name string) error { return clearProfile(name, false) }

func clearProfile(name string, remove bool) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(GlobalConfigDir(), 0o700); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(GlobalConfigDir(), "config.lock"), flock.SetPermissions(0o600))
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("locking global config: %w", err)
	}
	defer func() { _ = lock.Close() }()
	data, err := os.ReadFile(filepath.Join(GlobalConfigDir(), "config.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var doc yaml.Node
	if parseErr := yaml.Unmarshal(data, &doc); parseErr != nil {
		return fmt.Errorf("reading global config: %w", parseErr)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("global config must be a mapping")
	}
	var cfg GlobalConfig
	if decodeErr := root.Decode(&cfg); decodeErr != nil {
		return decodeErr
	}
	if checkErr := checkProfileClear(&cfg, name, remove); checkErr != nil {
		return checkErr
	}
	var profiles *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "profiles" {
			profiles = root.Content[i+1]
			break
		}
	}
	if profiles == nil {
		profiles = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "profiles"}, profiles)
	}
	if profiles.Tag == "!!null" {
		*profiles = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if profiles.Kind != yaml.MappingNode {
		return fmt.Errorf("profiles must be a mapping")
	}
	found := false
	for i := 0; i < len(profiles.Content); i += 2 {
		if profiles.Content[i].Value == name {
			found = true
			if remove {
				profiles.Content = append(profiles.Content[:i], profiles.Content[i+2:]...)
			}
			break
		}
	}
	if !remove && !found {
		profiles.Content = append(profiles.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
	}
	// Move the store aside before changing config. A failed config write can
	// restore it; cleanup never traverses a replacement created concurrently.
	storeDir := filepath.Join(GlobalConfigDir(), "credentials", "profiles", name)
	// This sibling lock survives directory removal and is shared by credential
	// writes/refreshes. Holding it exclusively prevents stale lock inodes.
	if mkdirErr := os.MkdirAll(filepath.Dir(storeDir), 0o700); mkdirErr != nil {
		return mkdirErr
	}
	storeLock := flock.New(storeDir+".profile-lock", flock.SetPermissions(0o600))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	locked, err := storeLock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !locked {
		_ = storeLock.Close()
		if err == nil {
			err = fmt.Errorf("store lock unavailable")
		}
		return fmt.Errorf("waiting for credential operations in profile %s: %w", name, err)
	}
	defer func() { _ = storeLock.Close() }()
	var staged string
	if info, err := os.Lstat(storeDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("profile store %q must be a directory, not a symlink or file", name)
		}
		staged, err = os.MkdirTemp(filepath.Dir(storeDir), ".profile-clear-")
		if err != nil {
			return err
		}
		if renameErr := os.Rename(storeDir, filepath.Join(staged, "store")); renameErr != nil {
			_ = os.Remove(staged)
			return renameErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeProfileConfig(&doc); err != nil {
		if staged != "" {
			if restoreErr := os.Rename(filepath.Join(staged, "store"), storeDir); restoreErr != nil {
				return fmt.Errorf("saving profile: %w; could not restore credentials from %s: %v", err, staged, restoreErr)
			}
			_ = os.Remove(staged)
		}
		return err
	}
	if staged != "" {
		if err := os.RemoveAll(staged); err != nil {
			return fmt.Errorf("profile updated but could not clear old credentials at %s: %w", staged, err)
		}
	}
	return nil
}

// LoadProfileConfig reads profile metadata without requiring valid inheritance.
// This allows inspection and cleanup of broken chains; it never resolves values.
func LoadProfileConfig() (*GlobalConfig, error) {
	data, err := os.ReadFile(filepath.Join(GlobalConfigDir(), "config.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var cfg GlobalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("reading global config: %w", err)
	}
	for name := range cfg.Profiles {
		if err := ValidateProfileName(name); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

// CheckProfileClear validates a lifecycle operation without changing files.
func CheckProfileClear(name string, remove bool) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	cfg, err := LoadProfileConfig()
	if err != nil {
		return err
	}
	return checkProfileClear(cfg, name, remove)
}

func checkProfileClear(cfg *GlobalConfig, name string, remove bool) error {
	exists, err := cfg.ProfileExists(name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("profile %q does not exist", name)
	}
	if remove {
		var children []string
		for child, settings := range cfg.Profiles {
			if settings.Inherit == name {
				children = append(children, child)
			}
		}
		if len(children) > 0 {
			sort.Strings(children)
			return fmt.Errorf("profile %q is inherited by %s; update or remove those profiles first", name, strings.Join(children, ", "))
		}
	}
	return nil
}
