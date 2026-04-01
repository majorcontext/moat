package gatekeeper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfig_Full(t *testing.T) {
	yaml := `
proxy:
  port: 8080
  host: 127.0.0.1
tls:
  ca_cert: /tmp/ca.crt
  ca_key: /tmp/ca.key
credentials:
  - host: api.github.com
    header: Authorization
    grant: github
    source:
      type: env
      var: GITHUB_TOKEN
  - host: api.anthropic.com
    header: x-api-key
    grant: anthropic
    source:
      type: static
      value: sk-ant-123
  - host: api.example.com
    grant: aws-secret
    source:
      type: aws-secretsmanager
      secret: my-secret
      region: us-east-1
network:
  policy: strict
  allow:
    - "*.github.com"
    - api.anthropic.com
  rules:
    - host: api.example.com
      methods: [GET, POST]
policy:
  scope: tool-use
log:
  level: debug
  format: json
  output: stderr
  requests: /tmp/requests.jsonl
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	// Proxy
	if cfg.Proxy.Port != 8080 {
		t.Errorf("Proxy.Port = %d, want 8080", cfg.Proxy.Port)
	}
	if cfg.Proxy.Host != "127.0.0.1" {
		t.Errorf("Proxy.Host = %q, want 127.0.0.1", cfg.Proxy.Host)
	}

	// TLS
	if cfg.TLS.CACert != "/tmp/ca.crt" {
		t.Errorf("TLS.CACert = %q, want /tmp/ca.crt", cfg.TLS.CACert)
	}
	if cfg.TLS.CAKey != "/tmp/ca.key" {
		t.Errorf("TLS.CAKey = %q, want /tmp/ca.key", cfg.TLS.CAKey)
	}

	// Credentials
	if len(cfg.Credentials) != 3 {
		t.Fatalf("len(Credentials) = %d, want 3", len(cfg.Credentials))
	}
	if cfg.Credentials[0].Host != "api.github.com" {
		t.Errorf("Credentials[0].Host = %q, want api.github.com", cfg.Credentials[0].Host)
	}
	if cfg.Credentials[0].Header != "Authorization" {
		t.Errorf("Credentials[0].Header = %q, want Authorization", cfg.Credentials[0].Header)
	}
	if cfg.Credentials[0].Grant != "github" {
		t.Errorf("Credentials[0].Grant = %q, want github", cfg.Credentials[0].Grant)
	}
	if cfg.Credentials[0].Source.Type != "env" {
		t.Errorf("Credentials[0].Source.Type = %q, want env", cfg.Credentials[0].Source.Type)
	}
	if cfg.Credentials[0].Source.Var != "GITHUB_TOKEN" {
		t.Errorf("Credentials[0].Source.Var = %q, want GITHUB_TOKEN", cfg.Credentials[0].Source.Var)
	}
	if cfg.Credentials[1].Host != "api.anthropic.com" {
		t.Errorf("Credentials[1].Host = %q, want api.anthropic.com", cfg.Credentials[1].Host)
	}
	if cfg.Credentials[1].Header != "x-api-key" {
		t.Errorf("Credentials[1].Header = %q, want x-api-key", cfg.Credentials[1].Header)
	}
	if cfg.Credentials[1].Source.Value != "sk-ant-123" {
		t.Errorf("Credentials[1].Source.Value = %q, want sk-ant-123", cfg.Credentials[1].Source.Value)
	}
	if cfg.Credentials[2].Source.Secret != "my-secret" {
		t.Errorf("Credentials[2].Source.Secret = %q, want my-secret", cfg.Credentials[2].Source.Secret)
	}

	// Network
	if cfg.Network.Policy != "strict" {
		t.Errorf("Network.Policy = %q, want strict", cfg.Network.Policy)
	}
	if len(cfg.Network.Allow) != 2 {
		t.Fatalf("len(Network.Allow) = %d, want 2", len(cfg.Network.Allow))
	}
	if cfg.Network.Allow[0] != "*.github.com" {
		t.Errorf("Network.Allow[0] = %q, want *.github.com", cfg.Network.Allow[0])
	}
	if len(cfg.Network.Rules) != 1 {
		t.Fatalf("len(Network.Rules) = %d, want 1", len(cfg.Network.Rules))
	}
	if cfg.Network.Rules[0].Host != "api.example.com" {
		t.Errorf("Network.Rules[0].Host = %q, want api.example.com", cfg.Network.Rules[0].Host)
	}
	if len(cfg.Network.Rules[0].Methods) != 2 {
		t.Fatalf("len(Network.Rules[0].Methods) = %d, want 2", len(cfg.Network.Rules[0].Methods))
	}

	// Policy
	if cfg.Policy["scope"] != "tool-use" {
		t.Errorf("Policy[scope] = %q, want tool-use", cfg.Policy["scope"])
	}

	// Log
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want json", cfg.Log.Format)
	}
}

func TestParseConfig_Minimal(t *testing.T) {
	yaml := `
proxy:
  port: 9090
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Proxy.Port != 9090 {
		t.Errorf("Proxy.Port = %d, want 9090", cfg.Proxy.Port)
	}
	if len(cfg.Credentials) != 0 {
		t.Errorf("len(Credentials) = %d, want 0", len(cfg.Credentials))
	}
}

func TestParseConfig_InvalidYAML(t *testing.T) {
	_, err := ParseConfig([]byte(`{{{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `
proxy:
  port: 7070
  host: 0.0.0.0
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Proxy.Port != 7070 {
		t.Errorf("Proxy.Port = %d, want 7070", cfg.Proxy.Port)
	}
}

func TestLoadConfig_NotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
