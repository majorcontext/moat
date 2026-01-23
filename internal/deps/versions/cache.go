package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultCacheTTL is the default time-to-live for cached version resolutions.
const DefaultCacheTTL = 24 * time.Hour

// CachedResolver wraps a Resolver with caching.
type CachedResolver struct {
	Resolver Resolver
	Cache    *Cache
	Runtime  string // "go", "node", "python"
}

// Resolve resolves a version, using the cache if available.
func (r *CachedResolver) Resolve(ctx context.Context, version string) (string, error) {
	key := fmt.Sprintf("%s@%s", r.Runtime, version)

	// Check cache first
	if resolved, ok := r.Cache.Get(key); ok {
		return resolved, nil
	}

	// Resolve and cache
	resolved, err := r.Resolver.Resolve(ctx, version)
	if err != nil {
		return "", err
	}

	r.Cache.Set(key, resolved)
	return resolved, nil
}

// Available returns available versions (not cached, as it's informational).
func (r *CachedResolver) Available(ctx context.Context) ([]string, error) {
	return r.Resolver.Available(ctx)
}

// LatestStable returns the latest stable version (cached).
func (r *CachedResolver) LatestStable(ctx context.Context) (string, error) {
	key := fmt.Sprintf("%s@latest", r.Runtime)

	if resolved, ok := r.Cache.Get(key); ok {
		return resolved, nil
	}

	resolved, err := r.Resolver.LatestStable(ctx)
	if err != nil {
		return "", err
	}

	r.Cache.Set(key, resolved)
	return resolved, nil
}

// Cache stores resolved versions with TTL.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
	path    string // Path to persist cache
}

type cacheEntry struct {
	Version    string    `json:"version"`
	ResolvedAt time.Time `json:"resolved_at"`
}

type cacheFile struct {
	Entries map[string]cacheEntry `json:"entries"`
	TTL     string                `json:"ttl"`
}

// NewCache creates a new cache with the given TTL.
// If path is non-empty, the cache will be persisted to disk.
func NewCache(ttl time.Duration, path string) *Cache {
	c := &Cache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
		path:    path,
	}

	if path != "" {
		if err := c.load(); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to load version cache, starting fresh",
				"path", path,
				"error", err)
		}
	}

	return c
}

// Get retrieves a cached version if it exists and hasn't expired.
func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}

	if time.Since(entry.ResolvedAt) > c.ttl {
		return "", false
	}

	return entry.Version, true
}

// Set stores a resolved version in the cache.
//
// Concurrency note: When multiple goroutines call Set concurrently, the in-memory
// cache is always consistent (protected by mutex), but the persisted file may not
// reflect the exact final state if writes interleave. This is acceptable for a
// cache with 24h TTL - on next startup, the cache will be reloaded and stale
// entries will be re-resolved as needed.
func (c *Cache) Set(key, version string) {
	c.mu.Lock()
	c.entries[key] = cacheEntry{
		Version:    version,
		ResolvedAt: time.Now(),
	}

	// Copy data for persistence outside the lock
	var data []byte
	var err error
	if c.path != "" {
		data, err = c.marshalLocked()
	}
	c.mu.Unlock()

	// Persist outside the lock to avoid blocking readers during I/O
	if c.path != "" && err == nil {
		if err := c.saveData(data); err != nil {
			slog.Warn("failed to persist version cache",
				"path", c.path,
				"error", err)
		}
	}
}

// Clear removes all entries from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]cacheEntry)

	if c.path != "" {
		_ = os.Remove(c.path)
	}
}

func (c *Cache) load() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}

	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return err
	}

	c.mu.Lock()
	c.entries = cf.Entries
	c.mu.Unlock()
	return nil
}

// marshalLocked serializes the cache entries while holding the lock.
// Must be called with c.mu held.
func (c *Cache) marshalLocked() ([]byte, error) {
	cf := cacheFile{
		Entries: c.entries,
		TTL:     c.ttl.String(),
	}
	return json.MarshalIndent(cf, "", "  ")
}

// saveData writes data to the cache file atomically.
// Uses write-to-temp-and-rename pattern to prevent corruption.
func (c *Cache) saveData(data []byte) error {
	// Ensure directory exists
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Write to temp file and rename atomically
	tmpFile := c.path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmpFile, c.path)
}

// DefaultCache returns a cache using the default moat cache directory.
func DefaultCache() *Cache {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to in-memory only
		return NewCache(DefaultCacheTTL, "")
	}

	path := filepath.Join(home, ".moat", "cache", "versions.json")
	return NewCache(DefaultCacheTTL, path)
}

// CachedResolverFor returns a cached resolver for a runtime.
func CachedResolverFor(runtime string, cache *Cache) Resolver {
	resolver := ResolverFor(runtime)
	if resolver == nil {
		return nil
	}

	return &CachedResolver{
		Resolver: resolver,
		Cache:    cache,
		Runtime:  runtime,
	}
}
