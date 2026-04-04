# Host Traffic Blocking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Block host-bound traffic by default (even in permissive mode) and add `network.host` for per-port opt-in, plus a `MOAT_HOST_GATEWAY` env var for portable host addressing.

**Architecture:** The proxy gains a host-gateway check in `checkNetworkPolicyForRequest` that blocks traffic to the host gateway address unless the target port is in an allow-list. The host gateway address and allowed ports flow from `moat.yaml` → config → run manager → daemon register request → run context → proxy RunContextData. A `MOAT_HOST_GATEWAY` env var is set in every container.

**Tech Stack:** Go, existing proxy/daemon/config infrastructure

**Testing strategy:** Unit tests can run inside this container with `make test-unit`. E2e tests require a container runtime on the host (`go test -tags=e2e ./internal/e2e/`). Each task includes TDD — write the failing test first, then implement.

---

### Task 1: Config — parse `network.host` from moat.yaml

**Files:**
- Modify: `internal/config/config.go:237-242` (NetworkConfig struct)
- Modify: `internal/config/config.go:525-537` (validation in Load)
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests for `network.host` parsing**

Add to `internal/config/config_test.go`:

```go
func TestNetworkHostConfig(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []int
		wantErr string
	}{
		{
			name: "single port",
			yaml: "agent: test\nnetwork:\n  host:\n    - 8288\n",
			want: []int{8288},
		},
		{
			name: "multiple ports",
			yaml: "agent: test\nnetwork:\n  host:\n    - 8288\n    - 5432\n",
			want: []int{8288, 5432},
		},
		{
			name: "omitted means empty",
			yaml: "agent: test\n",
			want: nil,
		},
		{
			name:    "port zero",
			yaml:    "agent: test\nnetwork:\n  host:\n    - 0\n",
			wantErr: "network.host",
		},
		{
			name:    "port too high",
			yaml:    "agent: test\nnetwork:\n  host:\n    - 70000\n",
			wantErr: "network.host",
		},
		{
			name:    "negative port",
			yaml:    "agent: test\nnetwork:\n  host:\n    - -1\n",
			wantErr: "network.host",
		},
		{
			name:    "duplicate port",
			yaml:    "agent: test\nnetwork:\n  host:\n    - 8288\n    - 8288\n",
			wantErr: "network.host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(tt.yaml), 0644)
			cfg, err := Load(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cfg.Network.Host) != len(tt.want) {
				t.Fatalf("got %d host ports, want %d", len(cfg.Network.Host), len(tt.want))
			}
			for i, p := range tt.want {
				if cfg.Network.Host[i] != p {
					t.Errorf("host[%d] = %d, want %d", i, cfg.Network.Host[i], p)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test-unit ARGS='-run TestNetworkHostConfig'`
Expected: FAIL — `cfg.Network.Host` field does not exist.

- [ ] **Step 3: Add `Host` field to `NetworkConfig` and validation**

In `internal/config/config.go`, add `Host` to the struct:

```go
type NetworkConfig struct {
	Policy     string                      `yaml:"policy,omitempty"`
	Allow      []string                    `yaml:"allow,omitempty"`
	Rules      []netrules.NetworkRuleEntry `yaml:"rules,omitempty"`
	Host       []int                       `yaml:"host,omitempty"`
	KeepPolicy *keep.PolicyConfig          `yaml:"keep_policy,omitempty"`
}
```

In the `Load` function, after the network policy validation block (after the `network.allow` error check around line 537), add validation:

```go
// Validate network.host ports
seen := make(map[int]bool, len(cfg.Network.Host))
for _, port := range cfg.Network.Host {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("network.host: port %d is out of range (1-65535)", port)
	}
	if seen[port] {
		return nil, fmt.Errorf("network.host: duplicate port %d", port)
	}
	seen[port] = true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test-unit ARGS='-run TestNetworkHostConfig'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): parse network.host for per-port host access control"
```

---

### Task 2: Daemon API — add host gateway and allowed ports to RegisterRequest

**Files:**
- Modify: `internal/daemon/api.go:64-80` (RegisterRequest)
- Modify: `internal/daemon/api.go:126-148` (ToRunContext)
- Modify: `internal/daemon/runcontext.go:50-76` (RunContext)
- Test: `internal/daemon/api_test.go`

- [ ] **Step 1: Write failing test for host gateway fields in ToRunContext**

Add to `internal/daemon/api_test.go`:

```go
func TestToRunContext_HostGateway(t *testing.T) {
	req := RegisterRequest{
		RunID:            "run_host_test",
		HostGateway:      "host.docker.internal",
		AllowedHostPorts: []int{8288, 5432},
	}
	rc := req.ToRunContext()

	if rc.HostGateway != "host.docker.internal" {
		t.Errorf("HostGateway = %q, want %q", rc.HostGateway, "host.docker.internal")
	}
	if len(rc.AllowedHostPorts) != 2 || rc.AllowedHostPorts[0] != 8288 || rc.AllowedHostPorts[1] != 5432 {
		t.Errorf("AllowedHostPorts = %v, want [8288 5432]", rc.AllowedHostPorts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit ARGS='-run TestToRunContext_HostGateway'`
Expected: FAIL — fields do not exist.

- [ ] **Step 3: Add fields to RegisterRequest, RunContext, and ToRunContext**

In `internal/daemon/api.go`, add to `RegisterRequest`:

```go
HostGateway      string `json:"host_gateway,omitempty"`
AllowedHostPorts []int  `json:"allowed_host_ports,omitempty"`
```

In `internal/daemon/api.go`, add to `ToRunContext()` (before the `return rc` line):

```go
rc.HostGateway = req.HostGateway
rc.AllowedHostPorts = req.AllowedHostPorts
```

In `internal/daemon/runcontext.go`, add to `RunContext`:

```go
HostGateway      string `json:"host_gateway,omitempty"`
AllowedHostPorts []int  `json:"allowed_host_ports,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-unit ARGS='-run TestToRunContext_HostGateway'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/api.go internal/daemon/api_test.go internal/daemon/runcontext.go
git commit -m "feat(daemon): add host gateway and allowed ports to RegisterRequest"
```

---

### Task 3: Proxy — add host gateway fields to RunContextData and blocking logic

**Files:**
- Modify: `internal/proxy/proxy.go:249-263` (RunContextData)
- Modify: `internal/proxy/proxy.go:847-867` (checkNetworkPolicyForRequest)
- Modify: `internal/proxy/proxy.go:1116-1123` (writeBlockedResponse — add host-specific variant)
- Test: `internal/proxy/proxy_test.go`

- [ ] **Step 1: Write failing tests for host gateway blocking**

Add to `internal/proxy/proxy_test.go`:

```go
func TestProxy_HostGatewayBlocked(t *testing.T) {
	// Start a backend that represents a "host service"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("host service"))
	}))
	defer backend.Close()

	backendURL := mustParseURL(backend.URL)
	backendHost := backendURL.Hostname()
	backendPort, _ := strconv.Atoi(backendURL.Port())

	p := NewProxy()
	p.SetContextResolver(func(token string) (*RunContextData, bool) {
		if token == "test_run" {
			return &RunContextData{
				Policy:      "permissive",
				HostGateway: backendHost, // treat backend as "host gateway"
			}, true
		}
		return nil, false
	})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
	}}

	req, _ := http.NewRequest("GET", backend.URL, nil)
	req.Header.Set("Proxy-Authorization", "Bearer test_run")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("expected 407 (blocked), got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), strconv.Itoa(backendPort)) {
		t.Errorf("blocked response should mention port %d, got: %s", backendPort, body)
	}
}

func TestProxy_HostGatewayAllowedPort(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("host service"))
	}))
	defer backend.Close()

	backendURL := mustParseURL(backend.URL)
	backendHost := backendURL.Hostname()
	backendPort, _ := strconv.Atoi(backendURL.Port())

	p := NewProxy()
	p.SetContextResolver(func(token string) (*RunContextData, bool) {
		if token == "test_run" {
			return &RunContextData{
				Policy:           "permissive",
				HostGateway:      backendHost,
				AllowedHostPorts: []int{backendPort},
			}, true
		}
		return nil, false
	})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
	}}

	req, _ := http.NewRequest("GET", backend.URL, nil)
	req.Header.Set("Proxy-Authorization", "Bearer test_run")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (allowed), got %d", resp.StatusCode)
	}
}

func TestProxy_HostGatewayStrictModeAlsoBlocks(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("host service"))
	}))
	defer backend.Close()

	backendURL := mustParseURL(backend.URL)
	backendHost := backendURL.Hostname()

	p := NewProxy()
	p.SetContextResolver(func(token string) (*RunContextData, bool) {
		if token == "test_run" {
			return &RunContextData{
				Policy:       "strict",
				AllowedHosts: []hostPattern{parseHostPattern(backendHost + ":" + backendURL.Port())},
				HostGateway:  backendHost,
				// No AllowedHostPorts — host gateway check takes precedence
			}, true
		}
		return nil, false
	})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
	}}

	req, _ := http.NewRequest("GET", backend.URL, nil)
	req.Header.Set("Proxy-Authorization", "Bearer test_run")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("expected 407 (blocked by host gateway even with strict allow), got %d", resp.StatusCode)
	}
}

func TestProxy_NonHostGatewayUnaffected(t *testing.T) {
	// A request to a non-host-gateway address should not be blocked
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("internet service"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetContextResolver(func(token string) (*RunContextData, bool) {
		if token == "test_run" {
			return &RunContextData{
				Policy:      "permissive",
				HostGateway: "host.docker.internal", // different from backend
			}, true
		}
		return nil, false
	})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
	}}

	req, _ := http.NewRequest("GET", backend.URL, nil)
	req.Header.Set("Proxy-Authorization", "Bearer test_run")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (not host gateway, should pass), got %d", resp.StatusCode)
	}
}

func TestProxy_HostGatewayNoContext(t *testing.T) {
	// Without RunContextData (legacy single-run mode), no host gateway blocking
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetNetworkPolicy("permissive", nil, nil)

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
	}}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (no context, no host blocking), got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test-unit ARGS='-run TestProxy_HostGateway'`
Expected: FAIL — `HostGateway` and `AllowedHostPorts` fields do not exist on `RunContextData`.

- [ ] **Step 3: Add fields and blocking logic**

In `internal/proxy/proxy.go`, add to `RunContextData`:

```go
HostGateway      string
AllowedHostPorts []int
```

Add a helper function (near the other policy helpers):

```go
// isHostGateway returns true if the given host matches the run's host gateway address.
func isHostGateway(rc *RunContextData, host string) bool {
	if rc == nil || rc.HostGateway == "" {
		return false
	}
	return host == rc.HostGateway
}

// isAllowedHostPort returns true if the port is in the run's allowed host ports list.
func isAllowedHostPort(rc *RunContextData, port int) bool {
	for _, p := range rc.AllowedHostPorts {
		if p == port {
			return true
		}
	}
	return false
}
```

Modify `checkNetworkPolicyForRequest` to add the host gateway check at the top of the `if rc != nil` block, before the existing policy checks:

```go
func (p *Proxy) checkNetworkPolicyForRequest(r *http.Request, host string, port int, method, path string) bool {
	if rc := getRunContext(r); rc != nil {
		// Block host-gateway traffic unless the port is explicitly allowed.
		// This applies regardless of permissive/strict policy.
		if isHostGateway(rc, host) {
			return isAllowedHostPort(rc, port)
		}

		if method != "CONNECT" && len(rc.HostRules) > 0 {
			return netrules.Check(rc.Policy, rc.HostRules, host, port, method, path, hostMatchAdapter)
		}
		if rc.Policy != "strict" {
			return true
		}
		return matchHost(rc.AllowedHosts, host, port)
	}

	p.mu.RLock()
	rules := p.hostRules
	policy := p.policy
	p.mu.RUnlock()

	if method != "CONNECT" && len(rules) > 0 {
		return netrules.Check(policy, rules, host, port, method, path, hostMatchAdapter)
	}
	return p.checkNetworkPolicy(host, port)
}
```

Add a host-specific blocked response writer. Modify `writeBlockedResponse` to accept a `hostBlocked` flag, or add a second function:

```go
// writeHostBlockedResponse writes a 407 response when a host-gateway request is blocked.
func (p *Proxy) writeHostBlockedResponse(w http.ResponseWriter, host string, port int) {
	w.Header().Set("X-Moat-Blocked", "host-service")
	w.Header().Set("Proxy-Authenticate", "Moat-Policy")
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusProxyAuthRequired)
	fmt.Fprintf(w, "Moat: request blocked — host service access is not allowed by default.\n"+
		"To allow port %d on the host, add to moat.yaml:\n\n"+
		"  network:\n    host:\n      - %d\n", port, port)
}
```

Now update `handleHTTP` and `handleConnect` to use the host-specific message. In `handleHTTP`, replace the existing policy block check (around line 1151) with:

```go
if !p.checkNetworkPolicyForRequest(r, host, port, r.Method, r.URL.Path) {
	duration := time.Since(start)
	p.logRequest(r, r.Method, r.URL.String(), http.StatusProxyAuthRequired, duration, nil, originalReqHeaders, nil, reqBody, nil, nil)
	rc := getRunContext(r)
	if rc != nil && isHostGateway(rc, host) {
		p.logPolicy(r, "network", "http.request", "", "Host service blocked: "+host+":"+strconv.Itoa(port))
		p.writeHostBlockedResponse(w, host, port)
	} else {
		p.logPolicy(r, "network", "http.request", "", "Host not in allow list: "+host)
		p.writeBlockedResponse(w, host)
	}
	return
}
```

Apply the same pattern in `handleConnect` (around line 1249). Read the current code to get exact line references — the pattern is the same: check if host gateway, use host-specific message.

Ensure `strconv` is imported (it likely already is, but verify).

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test-unit ARGS='-run TestProxy_HostGateway'`
Expected: All 5 tests PASS.

- [ ] **Step 5: Run all proxy tests to check for regressions**

Run: `make test-unit ARGS='-run TestProxy_'`
Expected: All existing proxy tests still pass. The key regression risk is `TestProxy_NetworkPolicyPermissive` — it uses no RunContextData, so host gateway blocking doesn't apply and it should still pass.

- [ ] **Step 6: Commit**

```bash
git add internal/proxy/proxy.go internal/proxy/proxy_test.go
git commit -m "feat(proxy): block host-gateway traffic by default with per-port opt-in"
```

---

### Task 4: Daemon — propagate host gateway to RunContextData

**Files:**
- Modify: `internal/daemon/runcontext.go:283-378` (ToProxyContextData)
- Test: `internal/daemon/runcontext_test.go` (or `api_test.go`)

- [ ] **Step 1: Write failing test**

Find the existing test file for `ToProxyContextData`. Add a test:

```go
func TestRunContext_ToProxyContextData_HostGateway(t *testing.T) {
	rc := NewRunContext("run_host_test")
	rc.HostGateway = "host.docker.internal"
	rc.AllowedHostPorts = []int{8288, 5432}

	d := rc.ToProxyContextData()

	if d.HostGateway != "host.docker.internal" {
		t.Errorf("HostGateway = %q, want %q", d.HostGateway, "host.docker.internal")
	}
	if len(d.AllowedHostPorts) != 2 || d.AllowedHostPorts[0] != 8288 || d.AllowedHostPorts[1] != 5432 {
		t.Errorf("AllowedHostPorts = %v, want [8288 5432]", d.AllowedHostPorts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit ARGS='-run TestRunContext_ToProxyContextData_HostGateway'`
Expected: FAIL — the fields aren't propagated yet.

- [ ] **Step 3: Propagate in ToProxyContextData**

In `internal/daemon/runcontext.go`, in `ToProxyContextData()`, add after the `d.KeepEngines = rc.KeepEngines` line (near end of function, before `return d`):

```go
// Propagate host gateway config for host traffic blocking.
d.HostGateway = rc.HostGateway
if len(rc.AllowedHostPorts) > 0 {
	d.AllowedHostPorts = make([]int, len(rc.AllowedHostPorts))
	copy(d.AllowedHostPorts, rc.AllowedHostPorts)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-unit ARGS='-run TestRunContext_ToProxyContextData_HostGateway'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/runcontext.go internal/daemon/runcontext_test.go
git commit -m "feat(daemon): propagate host gateway to proxy RunContextData"
```

Note: if `runcontext_test.go` doesn't exist, check which test file contains `ToProxyContextData` tests and add there. Use `grep -r "ToProxyContextData" internal/daemon/*_test.go` to find it.

---

### Task 5: Run manager — set MOAT_HOST_GATEWAY and pass host config to daemon

**Files:**
- Modify: `internal/run/manager.go` (env var setup and register request)
- Test: `internal/run/manager_test.go`

- [ ] **Step 1: Write failing test for MOAT_HOST_GATEWAY env var**

Find the existing `TestNetworkPolicyConfiguration` test in `internal/run/manager_test.go`. Add a new test:

```go
func TestHostGatewayEnvVar(t *testing.T) {
	// This test verifies that MOAT_HOST_GATEWAY is set in container env.
	// The actual value depends on the runtime, so we test the wiring
	// by checking buildRegisterRequest propagates host config.
	rc := daemon.NewRunContext("run_test")
	rc.HostGateway = "host.docker.internal"
	rc.AllowedHostPorts = []int{8288}

	req := buildRegisterRequest(rc, nil)

	if req.HostGateway != "host.docker.internal" {
		t.Errorf("HostGateway = %q, want %q", req.HostGateway, "host.docker.internal")
	}
	if len(req.AllowedHostPorts) != 1 || req.AllowedHostPorts[0] != 8288 {
		t.Errorf("AllowedHostPorts = %v, want [8288]", req.AllowedHostPorts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit ARGS='-run TestHostGatewayEnvVar'`
Expected: FAIL — `buildRegisterRequest` doesn't include host gateway fields.

- [ ] **Step 3: Wire up host gateway in run manager**

In `internal/run/manager.go`, find `buildRegisterRequest` (around line 3738). Add to the `RegisterRequest` construction:

```go
req := daemon.RegisterRequest{
	RunID:            rc.RunID,
	NetworkPolicy:    rc.NetworkPolicy,
	NetworkAllow:     rc.NetworkAllow,
	NetworkRules:     rc.NetworkRules,
	HostGateway:      rc.HostGateway,
	AllowedHostPorts: rc.AllowedHostPorts,
	MCPServers:       rc.MCPServers,
	Grants:           grants,
	AWSConfig:        rc.AWSConfig,
}
```

Find where network policy is set on the RunContext (around line 697). Add host config:

```go
if opts.Config != nil {
	runCtx.NetworkPolicy = opts.Config.Network.Policy
	// ... existing NetworkRules/NetworkAllow code ...

	// Set host gateway and allowed ports
	runCtx.AllowedHostPorts = opts.Config.Network.Host
}
```

Find where `hostAddr` is determined from `m.runtime.GetHostAddress()` (around line 837). After that line, set both the env var and the RunContext field:

```go
hostAddr = m.runtime.GetHostAddress()

// Set host gateway for proxy blocking and container env var
runCtx.HostGateway = hostAddr
```

Find where `proxyEnv` is built (after the proxy URL construction). Add the env var. Place it near other `MOAT_` env vars:

```go
proxyEnv = append(proxyEnv, "MOAT_HOST_GATEWAY="+hostAddr)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-unit ARGS='-run TestHostGatewayEnvVar'`
Expected: PASS

- [ ] **Step 5: Run all manager tests for regressions**

Run: `make test-unit ARGS='-run TestNetworkPolicy'`
Expected: All existing tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/run/manager.go internal/run/manager_test.go
git commit -m "feat(run): set MOAT_HOST_GATEWAY env var and pass host config to daemon"
```

---

### Task 6: Handle CONNECT requests for host gateway blocking

**Files:**
- Modify: `internal/proxy/proxy.go` (handleConnect)
- Test: `internal/proxy/proxy_test.go`

The `handleConnect` method is used for HTTPS tunneling. The host gateway check in `checkNetworkPolicyForRequest` already handles this (added in Task 3), but we need to ensure the blocked response message is correct for CONNECT too.

- [ ] **Step 1: Write failing test for CONNECT host gateway blocking**

```go
func TestProxy_HostGatewayBlockedCONNECT(t *testing.T) {
	// HTTPS requests use CONNECT. Verify the host gateway check works for CONNECT.
	p := NewProxy()
	p.SetContextResolver(func(token string) (*RunContextData, bool) {
		if token == "test_run" {
			return &RunContextData{
				Policy:      "permissive",
				HostGateway: "host.docker.internal",
			}, true
		}
		return nil, false
	})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	proxyURL := mustParseURL(proxyServer.URL)

	// Send a CONNECT request for host.docker.internal:443
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Write CONNECT with proxy auth
	fmt.Fprintf(conn, "CONNECT host.docker.internal:443 HTTP/1.1\r\n")
	fmt.Fprintf(conn, "Host: host.docker.internal:443\r\n")
	fmt.Fprintf(conn, "Proxy-Authorization: Bearer test_run\r\n")
	fmt.Fprintf(conn, "\r\n")

	// Read response
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("expected 407, got %d", resp.StatusCode)
	}
}
```

Note: This test requires `"bufio"` and `"fmt"` and `"net"` imports. Check which are already imported in the test file.

- [ ] **Step 2: Run test to verify it fails or passes**

Run: `make test-unit ARGS='-run TestProxy_HostGatewayBlockedCONNECT'`

This may already pass from the Task 3 changes since `checkNetworkPolicyForRequest` is called in `handleConnect`. If it passes, verify the response body mentions host service blocking (not generic network policy). If it doesn't, update `handleConnect` similarly to how `handleHTTP` was updated in Task 3.

- [ ] **Step 3: Update handleConnect if needed**

Read `handleConnect` to find where the blocked response is written (around line 1249-1255). Apply the same host-gateway-specific message pattern:

```go
if !p.checkNetworkPolicyForRequest(r, host, port, "CONNECT", "") {
	if p.logger != nil {
		p.logRequest(r, r.Method, r.Host, http.StatusProxyAuthRequired, 0, nil, nil, nil, nil, nil, nil)
	}
	rc := getRunContext(r)
	if rc != nil && isHostGateway(rc, host) {
		p.logPolicy(r, "network", "http.connect", "", "Host service blocked: "+host+":"+strconv.Itoa(port))
		p.writeHostBlockedResponse(w, host, port)
	} else {
		p.logPolicy(r, "network", "http.connect", "", "Host not in allow list: "+host)
		p.writeBlockedResponse(w, host)
	}
	return
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-unit ARGS='-run TestProxy_HostGatewayBlockedCONNECT'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/proxy.go internal/proxy/proxy_test.go
git commit -m "feat(proxy): host-specific blocked message for CONNECT requests"
```

---

### Task 7: Linux edge case — implicitly allow proxy port

**Files:**
- Modify: `internal/run/manager.go`
- Test: `internal/run/manager_test.go`

On Linux with Docker host network mode, `GetHostAddress()` returns `127.0.0.1`. The proxy also listens on `127.0.0.1`. If the proxy port isn't in `AllowedHostPorts`, the proxy would block its own traffic. In practice, proxy-internal routes (`/mcp/*`, `/relay/*`, `/_aws/*`) are direct requests and don't go through `checkNetworkPolicyForRequest`. But to be safe, add the proxy port to `AllowedHostPorts` when the host address is `127.0.0.1`.

- [ ] **Step 1: Write failing test**

```go
func TestHostGatewayLinuxProxyPort(t *testing.T) {
	// On Linux, host gateway is 127.0.0.1. The proxy port must be
	// implicitly allowed to prevent self-blocking.
	rc := daemon.NewRunContext("run_linux_test")
	rc.HostGateway = "127.0.0.1"
	rc.AllowedHostPorts = []int{8288}

	// Simulate: proxy port is 12345, should be added
	addProxyPortIfNeeded(rc, 12345)

	if len(rc.AllowedHostPorts) != 2 {
		t.Fatalf("expected 2 ports, got %d: %v", len(rc.AllowedHostPorts), rc.AllowedHostPorts)
	}
	found := false
	for _, p := range rc.AllowedHostPorts {
		if p == 12345 {
			found = true
		}
	}
	if !found {
		t.Errorf("proxy port 12345 not in AllowedHostPorts: %v", rc.AllowedHostPorts)
	}
}

func TestHostGatewayNonLinuxNoProxyPort(t *testing.T) {
	// When host gateway is not 127.0.0.1, don't add proxy port
	rc := daemon.NewRunContext("run_mac_test")
	rc.HostGateway = "host.docker.internal"
	rc.AllowedHostPorts = []int{8288}

	addProxyPortIfNeeded(rc, 12345)

	if len(rc.AllowedHostPorts) != 1 {
		t.Fatalf("expected 1 port (unchanged), got %d: %v", len(rc.AllowedHostPorts), rc.AllowedHostPorts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test-unit ARGS='-run TestHostGatewayLinuxProxyPort'`
Expected: FAIL — `addProxyPortIfNeeded` does not exist.

- [ ] **Step 3: Implement addProxyPortIfNeeded**

In `internal/run/manager.go`, add:

```go
// addProxyPortIfNeeded adds the proxy port to AllowedHostPorts when the host
// gateway is 127.0.0.1 (Linux). This prevents the proxy from blocking its own
// traffic when it shares the loopback address with the host gateway.
func addProxyPortIfNeeded(rc *daemon.RunContext, proxyPort int) {
	if rc.HostGateway != "127.0.0.1" {
		return
	}
	for _, p := range rc.AllowedHostPorts {
		if p == proxyPort {
			return // already present
		}
	}
	rc.AllowedHostPorts = append(rc.AllowedHostPorts, proxyPort)
}
```

Call it in the run manager after setting `runCtx.HostGateway` and after getting the proxy port from the daemon response:

```go
// On Linux, proxy port shares 127.0.0.1 with host gateway.
// Add it to allowed ports to prevent self-blocking.
addProxyPortIfNeeded(runCtx, regResp.ProxyPort)
```

Wait — the proxy port comes from `regResp.ProxyPort` which is returned *after* registration. So the proxy port needs to be added to the RunContext *after* registration but *before* the run context is used. Look at the flow:

1. `runCtx` is built with config
2. `buildRegisterRequest(runCtx, grants)` creates the request
3. Daemon returns `regResp.ProxyPort`
4. But the RunContext was already sent to the daemon

This means the proxy port addition must happen in the **daemon** side, not the CLI side. The daemon knows its own proxy port. Let's adjust:

In `internal/daemon/server.go`, find where the register handler processes the request (look for `handleRegisterRun` or similar). After converting to RunContext, add:

```go
// On Linux (host gateway is 127.0.0.1), the proxy port shares the same
// address. Add it to allowed host ports to prevent self-blocking.
if rc.HostGateway == "127.0.0.1" {
	proxyPort := s.proxyPort // the daemon knows its own port
	found := false
	for _, p := range rc.AllowedHostPorts {
		if p == proxyPort {
			found = true
			break
		}
	}
	if !found {
		rc.AllowedHostPorts = append(rc.AllowedHostPorts, proxyPort)
	}
}
```

Update the test to match the actual implementation location. If the logic is in the daemon server, the unit test should test the daemon's register handler or a helper function extracted from it.

Alternatively, keep it simpler: extract a package-level function in `daemon` and test it directly:

In `internal/daemon/server.go`, add:

```go
// addProxyPortForLoopback adds the proxy port to AllowedHostPorts when the
// host gateway is 127.0.0.1 (Linux Docker with host networking). This prevents
// the proxy from blocking its own traffic.
func addProxyPortForLoopback(rc *RunContext, proxyPort int) {
	if rc.HostGateway != "127.0.0.1" {
		return
	}
	for _, p := range rc.AllowedHostPorts {
		if p == proxyPort {
			return
		}
	}
	rc.AllowedHostPorts = append(rc.AllowedHostPorts, proxyPort)
}
```

Call it in `handleRegisterRun` after `req.ToRunContext()`.

Move the tests to `internal/daemon/server_test.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-unit ARGS='-run TestHostGateway.*ProxyPort'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/server.go internal/daemon/server_test.go
git commit -m "fix(daemon): implicitly allow proxy port on Linux loopback host gateway"
```

---

### Task 8: Documentation and changelog

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Update moat.yaml reference**

In `docs/content/reference/02-moat-yaml.md`, find the `network` section. Add documentation for `host`:

```markdown
### `network.host`

List of TCP ports on the host machine that the container may access. By default, all traffic to the host is blocked — even in permissive mode.

```yaml
network:
  host:
    - 8288    # Inngest dev server
    - 5432    # PostgreSQL
```

The container can reach these ports using the `MOAT_HOST_GATEWAY` environment variable:

```bash
curl http://$MOAT_HOST_GATEWAY:8288/api/v1/events
```

`MOAT_HOST_GATEWAY` is set automatically in every container and resolves to the correct host address regardless of runtime (Docker or Apple containers).
```

- [ ] **Step 2: Update CHANGELOG.md**

Add under the current unreleased version (or create a new section):

```markdown
### Breaking

- **Host traffic blocked by default** — containers can no longer reach services on the host machine without explicit configuration, even in permissive network mode. To allow specific ports, add `network.host` to `moat.yaml`:

  ```yaml
  network:
    host:
      - 8288
  ```

### Added

- **`network.host`** — per-port access control for host services. Declare which ports on the host the container may reach. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
- **`MOAT_HOST_GATEWAY` env var** — set in every container, provides the portable host address (`host.docker.internal` on Docker, gateway IP on Apple containers).
```

- [ ] **Step 3: Commit**

```bash
git add docs/content/reference/02-moat-yaml.md CHANGELOG.md
git commit -m "docs: add network.host reference and breaking change to changelog"
```

---

### Task 9: Lint and full test suite

- [ ] **Step 1: Run linter**

Run: `make lint`
Fix any issues found.

- [ ] **Step 2: Run full unit test suite**

Run: `make test-unit`
Expected: All tests pass. Key regression areas to watch:
- `TestProxy_NetworkPolicyPermissive` — no RunContextData, should be unaffected
- `TestProxy_PerContextNetworkPolicy` — uses RunContextData but no HostGateway set, should be unaffected
- `TestNetworkPolicyConfiguration` — existing manager tests
- `TestToRunContext` — existing daemon API test

- [ ] **Step 3: Fix any failures, commit fixes**

- [ ] **Step 4: Run e2e tests (on host only)**

These tests require a container runtime and must be run on the host machine, not inside this container:

```bash
go test -tags=e2e -v -run TestDaemonNetworkLogging ./internal/e2e/
go test -tags=e2e -v -run TestNetworkRequestsAreCaptured ./internal/e2e/
```

These verify that the proxy still forwards allowed traffic correctly through a real container.

- [ ] **Step 5: Final commit if needed**

```bash
git add -A
git commit -m "fix: address lint and test issues from host traffic blocking"
```
