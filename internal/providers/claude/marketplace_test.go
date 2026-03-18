package claude

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCollectMarketplaceTar(t *testing.T) {
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

	contextKey, tarData, err := CollectMarketplaceTar(dir, "my-marketplace")
	if err != nil {
		t.Fatal(err)
	}

	// Context key should be a flat tar filename
	if contextKey != "marketplace-my-marketplace.tar" {
		t.Errorf("expected context key 'marketplace-my-marketplace.tar', got %q", contextKey)
	}

	// Tar data should not be empty
	if len(tarData) == 0 {
		t.Fatal("tar data should not be empty")
	}

	// Extract tar and verify contents
	files := extractTar(t, tarData)

	// marketplace.json should be present
	if content, ok := files[".claude-plugin/marketplace.json"]; !ok {
		t.Errorf("expected .claude-plugin/marketplace.json in tar, got keys: %v", tarKeys(files))
	} else if string(content) != `{"name":"test"}` {
		t.Errorf("unexpected marketplace.json content: %q", string(content))
	}

	// README should be present
	if content, ok := files["README.md"]; !ok {
		t.Errorf("expected README.md in tar, got keys: %v", tarKeys(files))
	} else if string(content) != "# Test" {
		t.Errorf("expected README content '# Test', got %q", string(content))
	}

	// .git/ contents should be excluded
	for key := range files {
		if key == ".git/HEAD" || key == ".git/" {
			t.Errorf(".git directory should be excluded, but found key %s", key)
		}
	}
}

func TestCollectMarketplaceTarEmptyDir(t *testing.T) {
	dir := t.TempDir()

	contextKey, tarData, err := CollectMarketplaceTar(dir, "empty")
	if err != nil {
		t.Fatal(err)
	}

	if contextKey != "marketplace-empty.tar" {
		t.Errorf("expected context key 'marketplace-empty.tar', got %q", contextKey)
	}

	// Tar should only contain the root directory entry
	files := extractTar(t, tarData)
	for name, content := range files {
		if len(content) > 0 {
			t.Errorf("expected no file entries in empty dir tar, got %s", name)
		}
	}
}

func TestCollectMarketplaceTarSkipsLargeFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a small file
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a file just over the 1MB limit
	largeContent := make([]byte, maxMarketplaceFileSize+1)
	if err := os.WriteFile(filepath.Join(dir, "large.bin"), largeContent, 0o644); err != nil {
		t.Fatal(err)
	}

	_, tarData, err := CollectMarketplaceTar(dir, "test")
	if err != nil {
		t.Fatal(err)
	}

	files := extractTar(t, tarData)

	if _, ok := files["small.txt"]; !ok {
		t.Error("small file should be included in tar")
	}
	if _, ok := files["large.bin"]; ok {
		t.Error("large file should be excluded from tar")
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

// extractTar reads a tar archive and returns a map of filename → content.
// Directory entries are included with nil content.
func extractTar(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading tar: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			files[hdr.Name] = nil
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading tar entry %s: %v", hdr.Name, err)
		}
		files[hdr.Name] = content
	}
	return files
}

// tarKeys returns the keys of a tar files map for diagnostic output.
func tarKeys(m map[string][]byte) []string {
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
