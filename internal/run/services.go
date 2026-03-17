package run

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/deps"
)

const passwordLength = 32
const passwordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const readinessTimeout = 30 * time.Second
const readinessInterval = 1 * time.Second

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
	if needsPassword {
		password, err := generatePassword()
		if err != nil {
			return container.ServiceConfig{}, err
		}
		if spec.Service.PasswordEnv != "" {
			env[spec.Service.PasswordEnv] = password
		} else {
			env["password"] = password
		}

		// Set extra_env from registry with placeholder substitution
		for k, v := range spec.Service.ExtraEnv {
			v = strings.ReplaceAll(v, "{db}", spec.Service.DefaultDB)
			v = strings.ReplaceAll(v, "{password}", password)
			env[k] = v
		}
	} else {
		// No auth — still apply extra_env without password substitution
		for k, v := range spec.Service.ExtraEnv {
			v = strings.ReplaceAll(v, "{db}", spec.Service.DefaultDB)
			env[k] = v
		}
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
	}, nil
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
