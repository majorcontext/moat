package container

import "strings"

// buildServiceInfo creates a ServiceInfo from a started container.
func buildServiceInfo(containerID string, cfg ServiceConfig) ServiceInfo {
	return ServiceInfo{
		ID:           containerID,
		Name:         cfg.Name,
		Host:         cfg.Name,
		Ports:        cfg.Ports,
		Env:          cfg.Env,
		ReadinessCmd: cfg.ReadinessCmd,
		PasswordEnv:  cfg.PasswordEnv,
	}
}

// resolvePlaceholders replaces {key} placeholders in template with values from
// env, matching keys case-insensitively (using lowercased keys). If passwordEnv
// is set (e.g. "POSTGRES_PASSWORD"), its value is also available as {password}.
func resolvePlaceholders(template string, env map[string]string, passwordEnv string) string {
	// If passwordEnv is set, make the value available under the {password} alias.
	if passwordEnv != "" {
		if pw, ok := env[passwordEnv]; ok {
			template = strings.ReplaceAll(template, "{password}", pw)
		}
	}
	for k, v := range env {
		template = strings.ReplaceAll(template, "{"+strings.ToLower(k)+"}", v)
	}
	return template
}
