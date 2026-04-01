package credentialsource

import (
	"context"
	"fmt"
	"os"
)

type envSource struct {
	varName string
}

// NewEnvSource creates a CredentialSource that reads from an environment variable.
func NewEnvSource(varName string) CredentialSource {
	return &envSource{varName: varName}
}

func (s *envSource) Fetch(_ context.Context) (string, error) {
	val := os.Getenv(s.varName)
	if val == "" {
		return "", fmt.Errorf("environment variable %s is not set", s.varName)
	}
	return val, nil
}

func (s *envSource) Type() string { return "env" }
