package run

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
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
	if userSpec != nil {
		// Check for DB override via common env vars
		if v, ok := userSpec.Env["POSTGRES_DB"]; ok {
			db = v
		}
		if v, ok := userSpec.Env["MYSQL_DATABASE"]; ok {
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

// buildServiceConfig creates a ServiceConfig for a service dependency.
// Populates both generic fields and service definition fields from the registry.
func buildServiceConfig(dep deps.Dependency, runID string, userSpec *config.ServiceSpec) (container.ServiceConfig, error) {
	spec, ok := deps.GetSpec(dep.Name)
	if !ok || spec.Service == nil {
		return container.ServiceConfig{}, fmt.Errorf("unknown service: %s", dep.Name)
	}

	password, err := generatePassword()
	if err != nil {
		return container.ServiceConfig{}, err
	}

	env := make(map[string]string)

	// Set password
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

	// Apply user overrides
	if userSpec != nil {
		for k, v := range userSpec.Env {
			env[k] = v
		}
	}

	return container.ServiceConfig{
		Name:         dep.Name,
		Version:      dep.Version,
		Env:          env,
		RunID:        runID,
		Image:        spec.Service.Image,
		Ports:        spec.Service.Ports,
		PasswordEnv:  spec.Service.PasswordEnv,
		ExtraCmd:     spec.Service.ExtraCmd,
		ReadinessCmd: spec.Service.ReadinessCmd,
	}, nil
}

// waitForServiceReady polls CheckReady until success or timeout.
func waitForServiceReady(ctx context.Context, mgr container.ServiceManager, info container.ServiceInfo) error {
	deadline := time.Now().Add(readinessTimeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if err := mgr.CheckReady(ctx, info); err != nil {
			lastErr = err
			time.Sleep(readinessInterval)
			continue
		}
		return nil
	}

	return fmt.Errorf("timed out after %s: %w", readinessTimeout, lastErr)
}
