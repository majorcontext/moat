package kiro

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func TestPrepareContainerWritesConfig(t *testing.T) {
	p := &Provider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		Credential:     &provider.Credential{Provider: "kiro", Token: "t"},
		ContainerHome:  "/home/moatuser",
		RuntimeContext: "# Moat context\nhello",
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{
			"local1": {Command: "mcp-local", Args: []string{"--x"}},
		},
		MCPServers: map[string]provider.MCPServerConfig{
			"remote1": {URL: "http://proxy/mcp/tok/remote1", Headers: map[string]string{"X-A": "b"}},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	dir := cfg.StagingDir

	cli := readJSON(t, filepath.Join(dir, "settings", "cli.json"))
	if cli["chat.disableTrustAllConfirmation"] != true {
		t.Errorf("cli.json missing chat.disableTrustAllConfirmation=true: %v", cli)
	}

	mcp := readJSON(t, filepath.Join(dir, "settings", "mcp.json"))
	servers, ok := mcp["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp.json mcpServers not an object: %v", mcp)
	}
	if _, ok := servers["local1"]; !ok {
		t.Error("mcp.json missing local1")
	}
	if _, ok := servers["remote1"]; !ok {
		t.Error("mcp.json missing remote1")
	}

	rawLocal, _ := json.Marshal(servers["local1"])
	if !strings.Contains(string(rawLocal), `"command"`) || strings.Contains(string(rawLocal), `"url"`) {
		t.Errorf("local1 should have stdio shape (command, no url): %s", rawLocal)
	}
	rawRemote, _ := json.Marshal(servers["remote1"])
	if !strings.Contains(string(rawRemote), `"url"`) || strings.Contains(string(rawRemote), `"command"`) {
		t.Errorf("remote1 should have HTTP shape (url, no command): %s", rawRemote)
	}

	agentCfg := readJSON(t, filepath.Join(dir, "agents", "default.json"))
	if agentCfg["includeMcpJson"] != true {
		t.Errorf("default.json includeMcpJson != true: %v", agentCfg)
	}
	if res, ok := agentCfg["resources"].([]any); !ok || len(res) == 0 {
		t.Errorf("default.json resources missing/empty: %v", agentCfg)
	}

	ctx, err := os.ReadFile(filepath.Join(dir, "steering", "moat-context.md"))
	if err != nil {
		t.Fatalf("steering/moat-context.md: %v", err)
	}
	if string(ctx) != "# Moat context\nhello" {
		t.Errorf("steering content = %q", string(ctx))
	}

	foundEnv := false
	for _, e := range cfg.Env {
		if e == "KIRO_API_KEY="+KiroAPIKeyPlaceholder {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("env missing KIRO_API_KEY placeholder: %v", cfg.Env)
	}
	if len(cfg.Mounts) != 1 || cfg.Mounts[0].Target != KiroInitMountPath || !cfg.Mounts[0].ReadOnly {
		t.Errorf("unexpected mounts: %+v", cfg.Mounts)
	}
}

func TestPrepareContainerOmitsEmptySteering(t *testing.T) {
	p := &Provider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		Credential:    &provider.Credential{Provider: "kiro", Token: "t"},
		ContainerHome: "/home/moatuser",
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()
	if _, err := os.Stat(filepath.Join(cfg.StagingDir, "steering", "moat-context.md")); !os.IsNotExist(err) {
		t.Errorf("steering file should not exist when RuntimeContext empty (err=%v)", err)
	}
}
