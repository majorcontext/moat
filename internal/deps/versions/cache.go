package versions

import (
	"context"
	"encoding/json"
	"fmt"
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
		_ = c.load() // Ignore errors on load, start fresh if corrupt
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
func (c *Cache) Set(key, version string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = cacheEntry{
		Version:    version,
		ResolvedAt: time.Now(),
	}

	if c.path != "" {
		_ = c.save() // Best effort persist
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

	c.entries = cf.Entries
	return nil
}

func (c *Cache) save() error {
	cf := cacheFile{
		Entries: c.entries,
		TTL:     c.ttl.String(),
	}

	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(c.path, data, 0o644)
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
