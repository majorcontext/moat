package gatekeeper

import (
	"fmt"

	"github.com/majorcontext/moat/internal/gatekeeper/credentialsource"
)

// ResolveSource creates a CredentialSource from a SourceConfig.
// Returns an error if the config contains fields not relevant to the selected type.
func ResolveSource(cfg SourceConfig) (credentialsource.CredentialSource, error) {
	switch cfg.Type {
	case "env":
		if cfg.Var == "" {
			return nil, fmt.Errorf("env source requires 'var' field")
		}
		if cfg.Value != "" || cfg.Secret != "" {
			return nil, fmt.Errorf("env source only uses 'var'; found extraneous fields")
		}
		return credentialsource.NewEnvSource(cfg.Var), nil
	case "static":
		if cfg.Value == "" {
			return nil, fmt.Errorf("static source requires 'value' field")
		}
		if cfg.Var != "" || cfg.Secret != "" {
			return nil, fmt.Errorf("static source only uses 'value'; found extraneous fields")
		}
		return credentialsource.NewStaticSource(cfg.Value), nil
	case "aws-secretsmanager":
		if cfg.Secret == "" {
			return nil, fmt.Errorf("aws-secretsmanager source requires 'secret' field")
		}
		if cfg.Var != "" || cfg.Value != "" {
			return nil, fmt.Errorf("aws-secretsmanager source only uses 'secret' and 'region'; found extraneous fields")
		}
		return credentialsource.NewAWSSecretsManagerSource(cfg.Secret, cfg.Region)
	default:
		return nil, fmt.Errorf("unknown credential source type: %q", cfg.Type)
	}
}
