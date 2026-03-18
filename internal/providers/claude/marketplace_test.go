package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCollectMarketplaceFiles(t *testing.T) {
	// Create a temp directory simulating a cloned marketplace repo.
	dir := t.TempDir()

	// Create .claude-plugin/marketplace.json
	pluginDir := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "marketplace.json"), []byte(`{"name":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create README.md at root
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create .git/HEAD (should be excluded)
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := CollectMarketplaceFiles(dir, "my-marketplace")
	if err != nil {
		t.Fatal(err)
	}

	// marketplace.json should be present with the right key
	key := "marketplaces/my-marketplace/.claude-plugin/marketplace.json"
	if _, ok := files[key]; !ok {
		t.Errorf("expected key %s in files, got keys: %v", key, mapKeys(files))
	}

	// README should be present
	readmeKey := "marketplaces/my-marketplace/README.md"
	if content, ok := files[readmeKey]; !ok {
		t.Errorf("expected key %s in files, got keys: %v", readmeKey, mapKeys(files))
	} else if string(content) != "# Test" {
		t.Errorf("expected README content '# Test', got %q", string(content))
	}

	// .git/ contents should be excluded
	gitKey := "marketplaces/my-marketplace/.git/HEAD"
	if _, ok := files[gitKey]; ok {
		t.Errorf(".git directory should be excluded, but found key %s", gitKey)
	}

	// Should have exactly 2 files
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), mapKeys(files))
	}
}

func TestCollectMarketplaceFilesEmptyDir(t *testing.T) {
	dir := t.TempDir()

	files, err := CollectMarketplaceFiles(dir, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("expected empty map, got %d files: %v", len(files), mapKeys(files))
	}
}

func TestGenerateKnownMarketplaces(t *testing.T) {
	marketplaces := []PreClonedMarketplace{
		{Name: "official", Source: "github", Repo: "anthropics/claude-plugins-official", LastUpdated: "2025-01-15T10:30:00+00:00"},
		{Name: "custom", Source: "git", Repo: "https://git.example.com/plugins.git", LastUpdated: "2025-02-20T14:00:00+00:00"},
	}

	data, err := GenerateKnownMarketplaces(marketplaces, "moatuser")
	if err != nil {
		t.Fatal(err)
	}

	// Parse the output
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("output is not valid JSON: %s", err)
	}

	// Should have two entries
	if len(result) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(result), jsonKeys(result))
	}

	// Check the github-source entry uses "repo" field
	var githubEntry struct {
		Source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			URL    string `json:"url"`
		} `json:"source"`
		InstallLocation string `json:"installLocation"`
		LastUpdated     string `json:"lastUpdated"`
	}
	if err := json.Unmarshal(result["official"], &githubEntry); err != nil {
		t.Fatalf("could not parse github entry: %s", err)
	}

	if githubEntry.Source.Source != "github" {
		t.Errorf("expected source.source 'github', got %q", githubEntry.Source.Source)
	}
	if githubEntry.Source.Repo != "anthropics/claude-plugins-official" {
		t.Errorf("expected source.repo 'anthropics/claude-plugins-official', got %q", githubEntry.Source.Repo)
	}
	if githubEntry.Source.URL != "" {
		t.Errorf("github source should not have url field, got %q", githubEntry.Source.URL)
	}

	expectedLocation := "/home/moatuser/.claude/plugins/marketplaces/official"
	if githubEntry.InstallLocation != expectedLocation {
		t.Errorf("expected installLocation %q, got %q", expectedLocation, githubEntry.InstallLocation)
	}

	if githubEntry.LastUpdated != "2025-01-15T10:30:00+00:00" {
		t.Errorf("expected lastUpdated '2025-01-15T10:30:00+00:00', got %q", githubEntry.LastUpdated)
	}

	// Check the git-source entry uses "url" field
	var gitEntry struct {
		Source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			URL    string `json:"url"`
		} `json:"source"`
		InstallLocation string `json:"installLocation"`
	}
	if err := json.Unmarshal(result["custom"], &gitEntry); err != nil {
		t.Fatalf("could not parse git entry: %s", err)
	}

	if gitEntry.Source.Source != "git" {
		t.Errorf("expected source.source 'git', got %q", gitEntry.Source.Source)
	}
	if gitEntry.Source.URL != "https://git.example.com/plugins.git" {
		t.Errorf("expected source.url 'https://git.example.com/plugins.git', got %q", gitEntry.Source.URL)
	}
	if gitEntry.Source.Repo != "" {
		t.Errorf("git source should not have repo field, got %q", gitEntry.Source.Repo)
	}

	expectedCustomLocation := "/home/moatuser/.claude/plugins/marketplaces/custom"
	if gitEntry.InstallLocation != expectedCustomLocation {
		t.Errorf("expected installLocation %q, got %q", expectedCustomLocation, gitEntry.InstallLocation)
	}
}

func TestGenerateKnownMarketplacesEmpty(t *testing.T) {
	data, err := GenerateKnownMarketplaces(nil, "moatuser")
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "{}" {
		t.Errorf("expected '{}', got %q", string(data))
	}
}

// mapKeys returns the keys of a map for diagnostic output.
func mapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// jsonKeys returns the keys of a JSON object map for diagnostic output.
func jsonKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
