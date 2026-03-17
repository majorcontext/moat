package run

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/deps"
)

const passwordLength = 32
const passwordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const readinessTimeout = 30 * time.Second
const readinessInterval = 1 * time.Second

// validProvisionItem matches safe provision item names (e.g., Ollama model names).
// Allows alphanumerics, dots, dashes, underscores, colons, slashes, and @ signs.
// Rejects shell metacharacters (;, |, $, `, &, (, ), etc.) to prevent injection
// when items are interpolated into sh -c commands.
var validProvisionItem = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@-]*$`)

// generatePassword creates a cryptographically random alphanumeric password.
func generatePassword() (string, error) {
	b := make([]byte, passwordLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordChars))))
		if err != nil {
			return "", fmt.Errorf("generating password: %w", err)
		}
		b[i] = passwordChars[n.Int64()]
	}
	return string(b), nil
}

// generateServiceEnv creates MOAT_* environment variables from service info and registry metadata.
func generateServiceEnv(def *deps.ServiceDef, info container.ServiceInfo, userSpec *config.ServiceSpec) map[string]string {
	prefix := "MOAT_" + def.EnvPrefix
	env := make(map[string]string)

	// Host
	env[prefix+"_HOST"] = info.Host

	// Ports
	for name, port := range info.Ports {
		portStr := strconv.Itoa(port)
		if name == "default" {
			env[prefix+"_PORT"] = portStr
		} else {
			env[prefix+"_"+strings.ToUpper(name)+"_PORT"] = portStr
		}
	}

	// User
	user := def.DefaultUser
	if user != "" {
		env[prefix+"_USER"] = user
	}

	// DB
	db := def.DefaultDB
	if userSpec != nil && def.DBEnv != "" {
		if v, ok := userSpec.Env[def.DBEnv]; ok {
			db = v
		}
	}
	if db != "" {
		env[prefix+"_DB"] = db
	}

	// Password
	password := ""
	if def.PasswordEnv != "" {
		password = info.Env[def.PasswordEnv]
	}
	if password == "" {
		password = info.Env["password"]
	}
	if password != "" {
		env[prefix+"_PASSWORD"] = password
	}

	// URL from template
	if def.URLFormat != "" {
		defaultPort := 0
		if p, ok := info.Ports["default"]; ok {
			defaultPort = p
		}
		url := def.URLFormat
		url = strings.ReplaceAll(url, "{scheme}", def.URLScheme)
		url = strings.ReplaceAll(url, "{user}", user)
		url = strings.ReplaceAll(url, "{password}", password)
		url = strings.ReplaceAll(url, "{host}", info.Host)
		url = strings.ReplaceAll(url, "{port}", strconv.Itoa(defaultPort))
		url = strings.ReplaceAll(url, "{db}", db)
		env[prefix+"_URL"] = url
	}

	return env
}

// serviceUsesPasswordPlaceholder reports whether a service's extra_cmd or
// readiness_cmd contains the {password} placeholder, indicating it needs a
// generated password even when password_env is empty (e.g., Redis).
func serviceUsesPasswordPlaceholder(svc *deps.ServiceDef) bool {
	if strings.Contains(svc.ReadinessCmd, "{password}") {
		return true
	}
	for _, arg := range svc.ExtraCmd {
		if strings.Contains(arg, "{password}") {
			return true
		}
	}
	return false
}

// buildServiceConfig creates a ServiceConfig for a service dependency.
// Populates both generic fields and service definition fields from the registry.
func buildServiceConfig(dep deps.Dependency, runID string, userSpec *config.ServiceSpec) (container.ServiceConfig, error) {
	spec, ok := deps.GetSpec(dep.Name)
	if !ok || spec.Service == nil {
		return container.ServiceConfig{}, fmt.Errorf("unknown service: %s", dep.Name)
	}
	if spec.Type != deps.TypeService {
		return container.ServiceConfig{}, fmt.Errorf("%s has type %q but expected %q", dep.Name, spec.Type, deps.TypeService)
	}

	env := make(map[string]string)

	// Determine if this service needs a password.
	// A service needs auth if it has a named password env var OR if its
	// extra_cmd / readiness_cmd reference the {password} placeholder (e.g., Redis).
	needsPassword := spec.Service.PasswordEnv != "" || serviceUsesPasswordPlaceholder(spec.Service)

	// Only generate password for services that have auth
	var password string
	if needsPassword {
		var err error
		password, err = generatePassword()
		if err != nil {
			return container.ServiceConfig{}, err
		}
		if spec.Service.PasswordEnv != "" {
			env[spec.Service.PasswordEnv] = password
		} else {
			env["password"] = password
		}
	}

	// Set extra_env from registry with placeholder substitution.
	// When password is empty, ReplaceAll is a no-op for {password}.
	for k, v := range spec.Service.ExtraEnv {
		v = strings.ReplaceAll(v, "{db}", spec.Service.DefaultDB)
		v = strings.ReplaceAll(v, "{password}", password)
		env[k] = v
	}

	// Apply user overrides
	if userSpec != nil {
		for k, v := range userSpec.Env {
			env[k] = v
		}
	}

	// Resolve provisions from user spec Extra using registry's provisions_key
	var provisions []string
	if userSpec != nil && spec.Service.ProvisionsKey != "" {
		// Check if key is present but was a scalar (nil) instead of a list
		if val, exists := userSpec.Extra[spec.Service.ProvisionsKey]; exists && val == nil {
			return container.ServiceConfig{}, fmt.Errorf(
				"services.%s.%s must be a list, not a scalar value",
				dep.Name, spec.Service.ProvisionsKey,
			)
		}
		provisions = userSpec.Extra[spec.Service.ProvisionsKey]

		// Validate: reject unknown Extra keys that don't match provisions_key
		for key := range userSpec.Extra {
			if key != spec.Service.ProvisionsKey {
				return container.ServiceConfig{}, fmt.Errorf(
					"services.%s.%s is not a valid key (did you mean %q?)",
					dep.Name, key, spec.Service.ProvisionsKey,
				)
			}
		}

		// Validate provision items to prevent shell injection.
		// Items are interpolated into sh -c commands; reject shell metacharacters.
		for _, item := range provisions {
			if !validProvisionItem.MatchString(item) {
				return container.ServiceConfig{}, fmt.Errorf(
					"invalid %s item %q: contains disallowed characters (must match %s)",
					spec.Service.ProvisionsKey, item, validProvisionItem.String(),
				)
			}
		}
	} else if userSpec != nil && len(userSpec.Extra) > 0 {
		// Service doesn't support provisions but user provided extra keys
		for key := range userSpec.Extra {
			return container.ServiceConfig{}, fmt.Errorf(
				"services.%s.%s is not a valid configuration key",
				dep.Name, key,
			)
		}
	}

	// Resolve cache host path
	var cacheHostPath string
	if spec.Service.CachePath != "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return container.ServiceConfig{}, fmt.Errorf("resolving home directory for cache: %w", err)
		}
		cacheHostPath = filepath.Join(homeDir, ".moat", "cache", dep.Name)
	}

	var memoryMB int
	if userSpec != nil {
		memoryMB = userSpec.Memory
	}

	return container.ServiceConfig{
		Name:          dep.Name,
		Version:       dep.Version,
		Env:           env,
		RunID:         runID,
		Image:         spec.Service.Image,
		Ports:         spec.Service.Ports,
		PasswordEnv:   spec.Service.PasswordEnv,
		ExtraCmd:      spec.Service.ExtraCmd,
		ReadinessCmd:  spec.Service.ReadinessCmd,
		CachePath:     spec.Service.CachePath,
		CacheHostPath: cacheHostPath,
		Provisions:    provisions,
		ProvisionCmd:  spec.Service.ProvisionCmd,
		MemoryMB:      memoryMB,
	}, nil
}

const provisionTimeout = 30 * time.Minute

// buildProvisionCmds creates concrete commands from a template and item list.
func buildProvisionCmds(cmdTemplate string, items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cmds := make([]string, len(items))
	for i, item := range items {
		cmds[i] = strings.ReplaceAll(cmdTemplate, "{item}", item)
	}
	return cmds
}

// provisionService runs provision commands inside a started service container.
// Uses flock-based advisory locking on the cache directory to prevent concurrent
// corruption from parallel runs. Each provision item gets its own timeout
// (provisionTimeout) rather than a single timeout for the entire batch.
func provisionService(ctx context.Context, mgr container.ServiceManager, info container.ServiceInfo, cfg container.ServiceConfig, stdout io.Writer) error {
	cmds := buildProvisionCmds(cfg.ProvisionCmd, cfg.Provisions)
	if len(cmds) == 0 {
		return nil
	}

	// Advisory lock on cache directory to prevent concurrent pull corruption.
	// Cache directory is already created by the manager before starting the sidecar.
	var lockFile *os.File
	if cfg.CacheHostPath != "" {
		lockPath := filepath.Join(cfg.CacheHostPath, ".lock")
		var err error
		lockFile, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("opening cache lock %s: %w", lockPath, err)
		}
		defer lockFile.Close()
	}

	for _, cmd := range cmds {
		if err := provisionItem(ctx, mgr, info, cmd, lockFile, stdout); err != nil {
			return err
		}
	}

	return nil
}

// provisionItem runs a single provision command with its own timeout and lock scope.
// The lock is shared across parallel moat runs — concurrent runs pulling different
// models will still serialize on this single lock file.
func provisionItem(ctx context.Context, mgr container.ServiceManager, info container.ServiceInfo, cmd string, lockFile *os.File, stdout io.Writer) error {
	cmdCtx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()

	// Acquire/release lock per-item to avoid holding it across the full batch.
	if lockFile != nil {
		if err := flockContext(cmdCtx, lockFile); err != nil {
			return err
		}
		defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
	}

	return mgr.ProvisionService(cmdCtx, info, []string{cmd}, stdout)
}

// flockContext acquires an exclusive flock, but respects context cancellation.
// syscall.Flock blocks indefinitely; this wraps it in a goroutine so the caller
// can bail out when the context expires.
//
// When context cancellation wins the select, the goroutine remains blocked on
// Flock until either the lock becomes available or the file descriptor is closed.
// The caller's deferred lockFile.Close() handles the latter — so the goroutine
// always terminates promptly. The buffered channel ensures it never blocks on send.
func flockContext(ctx context.Context, f *os.File) error {
	done := make(chan error, 1)
	go func() {
		done <- syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("acquiring cache lock: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("acquiring cache lock: %w", ctx.Err())
	}
}

// waitForServiceReady polls CheckReady until success or timeout.
func waitForServiceReady(ctx context.Context, mgr container.ServiceManager, info container.ServiceInfo) error {
	ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()

	ticker := time.NewTicker(readinessInterval)
	defer ticker.Stop()

	var lastErr error

	for {
		if err := mgr.CheckReady(ctx, info); err != nil {
			lastErr = err
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return fmt.Errorf("%w: last check: %w", ctx.Err(), lastErr)
			}
			continue
		}
		return nil
	}
}
