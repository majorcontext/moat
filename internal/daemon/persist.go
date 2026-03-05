package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/proxy"
)

// PersistedRun is the on-disk representation of a registered run.
// It stores grant names (not raw credentials) so provider tokens are never
// written to disk. The auth token (proxy-local random string) is stored in
// plaintext, protected by file permissions (0600). On restore, provider
// credentials are re-resolved from the encrypted credential store.
type PersistedRun struct {
	AuthToken        string                   `json:"auth_token"`
	RunID            string                   `json:"run_id"`
	ContainerID      string                   `json:"container_id,omitempty"`
	Grants           []string                 `json:"grants,omitempty"`
	MCPServers       []config.MCPServerConfig `json:"mcp_servers,omitempty"`
	NetworkPolicy    string                   `json:"network_policy,omitempty"`
	NetworkAllow     []string                 `json:"network_allow,omitempty"`
	AWSConfig        *AWSConfig               `json:"aws_config,omitempty"`
	TransformerSpecs []TransformerSpec        `json:"transformer_specs,omitempty"`
}

// persistedFile is the versioned on-disk format.
type persistedFile struct {
	Version int            `json:"version"`
	Runs    []PersistedRun `json:"runs"`
}

// RunPersister manages saving and loading the run registry to disk.
type RunPersister struct {
	path     string
	registry *Registry

	mu               sync.Mutex
	debounceTimer    *time.Timer
	debounceDuration time.Duration
	saveMu           sync.Mutex // serializes Save() calls
}

// NewRunPersister creates a persister that saves to the given file path.
func NewRunPersister(path string, registry *Registry) *RunPersister {
	return &RunPersister{
		path:             path,
		registry:         registry,
		debounceDuration: 50 * time.Millisecond,
	}
}

// Save serializes the current registry to disk atomically (temp file + rename).
// The file is written with 0600 permissions. Safe to call concurrently.
func (p *RunPersister) Save() error {
	entries := p.registry.List()
	runs := make([]PersistedRun, 0, len(entries))
	for _, rc := range entries {
		rc.mu.RLock()
		pr := PersistedRun{
			AuthToken:        rc.AuthToken,
			RunID:            rc.RunID,
			ContainerID:      rc.ContainerID,
			Grants:           rc.Grants,
			MCPServers:       rc.MCPServers,
			NetworkPolicy:    rc.NetworkPolicy,
			NetworkAllow:     rc.NetworkAllow,
			AWSConfig:        rc.AWSConfig,
			TransformerSpecs: rc.TransformerSpecs,
		}
		rc.mu.RUnlock()
		runs = append(runs, pr)
	}

	file := persistedFile{Version: 1, Runs: runs}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal persisted runs: %w", err)
	}

	p.saveMu.Lock()
	defer p.saveMu.Unlock()

	dir := filepath.Dir(p.path)
	tmp, err := os.CreateTemp(dir, ".runs-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, p.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// SaveDebounced coalesces rapid save calls with a short timer.
// Safe to call from multiple goroutines.
func (p *RunPersister) SaveDebounced() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.debounceTimer != nil {
		p.debounceTimer.Stop()
	}
	p.debounceTimer = time.AfterFunc(p.debounceDuration, func() {
		if err := p.Save(); err != nil {
			log.Warn("failed to persist run registry", "error", err)
		}
	})
}

// Flush stops any pending debounce timer and performs a synchronous Save.
// Call during daemon shutdown to ensure the final state is persisted.
func (p *RunPersister) Flush() error {
	p.mu.Lock()
	if p.debounceTimer != nil {
		p.debounceTimer.Stop()
		p.debounceTimer = nil
	}
	p.mu.Unlock()
	return p.Save()
}

// Load reads persisted runs from disk. Returns nil, nil if the file doesn't exist.
// Handles both the versioned format ({"version":1,"runs":[...]}) and the legacy
// bare-array format for backwards compatibility.
func (p *RunPersister) Load() ([]PersistedRun, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read persisted runs: %w", err)
	}

	// Try versioned format first.
	var file persistedFile
	if err := json.Unmarshal(data, &file); err == nil && file.Version > 0 {
		return file.Runs, nil
	}

	// Fall back to legacy bare-array format.
	var runs []PersistedRun
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("unmarshal persisted runs: %w", err)
	}
	return runs, nil
}

// RestoreRuns re-registers persisted runs by re-resolving credentials from
// the encrypted credential store. Returns the number of successfully restored runs.
func RestoreRuns(ctx context.Context, registry *Registry, runs []PersistedRun) int {
	if len(runs) == 0 {
		return 0
	}

	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		log.Warn("restore: cannot get encryption key, skipping restore", "error", err)
		return 0
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		log.Warn("restore: cannot open credential store, skipping restore", "error", err)
		return 0
	}

	restored := 0
	for _, pr := range runs {
		rc := NewRunContext(pr.RunID)
		rc.ContainerID = pr.ContainerID
		rc.Grants = pr.Grants
		rc.MCPServers = pr.MCPServers
		rc.NetworkPolicy = pr.NetworkPolicy
		rc.NetworkAllow = pr.NetworkAllow
		rc.AWSConfig = pr.AWSConfig
		rc.TransformerSpecs = pr.TransformerSpecs

		if err := resolveCredentials(rc, pr.Grants, pr.MCPServers, store); err != nil {
			log.Warn("restore: failed to resolve credentials, skipping run",
				"run_id", pr.RunID, "error", err)
			continue
		}

		// Create a per-run context and set cancel BEFORE registering so that
		// a concurrent handleUnregisterRun sees a valid cancel function.
		// This matches the ordering in handleRegisterRun.
		runCtx, cancel := context.WithCancel(ctx)
		rc.SetRefreshCancel(cancel)

		// Start token refresh if grants are present.
		if len(pr.Grants) > 0 {
			StartTokenRefresh(runCtx, rc, pr.Grants)
		}

		registry.RegisterWithToken(rc, pr.AuthToken)

		// Set up AWS credential provider if configured.
		if pr.AWSConfig != nil {
			awsProvider, awsErr := proxy.NewAWSCredentialProvider(
				runCtx,
				pr.AWSConfig.RoleARN,
				pr.AWSConfig.Region,
				pr.AWSConfig.SessionDuration,
				pr.AWSConfig.ExternalID,
				"moat-"+pr.RunID,
			)
			if awsErr != nil {
				log.Warn("restore: failed to create AWS credential provider",
					"run_id", pr.RunID, "error", awsErr)
			} else {
				awsProvider.SetAuthToken(pr.AuthToken)
				rc.SetAWSHandler(awsProvider.Handler())
			}
		}

		log.Info("restored run from disk",
			"run_id", pr.RunID,
			"container_id", pr.ContainerID,
			"grants", len(pr.Grants))
		restored++
	}
	return restored
}

// resolveCredentials re-resolves credentials from the store for all grants.
//
// WARNING: This must stay in sync with the credential setup in run/manager.go
// Create() (around the "Configure proxy with credentials" section). If you add
// a new provider setup step there, add the corresponding logic here too.
// A future refactor should extract a shared ConfigureRunFromGrants helper.
func resolveCredentials(rc *RunContext, grants []string, mcpServers []config.MCPServerConfig, store credential.Store) error {
	for _, grant := range grants {
		grantName := strings.Split(grant, ":")[0]
		if grantName == "ssh" {
			continue
		}

		credName := credential.Provider(provider.ResolveName(grantName))
		cred, err := store.Get(credName)
		if err != nil {
			return fmt.Errorf("grant %q: credential not found: %w", grantName, err)
		}
		provCred := provider.FromLegacy(cred)

		prov := provider.Get(grantName)
		if prov == nil {
			// MCP grant — match to MCP server config and set credential directly.
			matched := false
			for _, mcp := range mcpServers {
				if mcp.Auth != nil && mcp.Auth.Grant == grantName {
					serverHost := mcp.URL
					if u, parseErr := url.Parse(mcp.URL); parseErr == nil {
						serverHost = u.Host
					}
					rc.SetCredentialWithGrant(serverHost, mcp.Auth.Header, provCred.Token, grantName)
					matched = true
				}
			}
			if !matched {
				log.Warn("restore: no MCP server config found for grant", "grant", grantName)
			}
			continue
		}

		prov.ConfigureProxy(rc, provCred)
	}
	return nil
}
