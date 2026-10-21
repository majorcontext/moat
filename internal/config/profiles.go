package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	data, err = yaml.Marshal(&doc)
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
	return os.Rename(f.Name(), path)
}
