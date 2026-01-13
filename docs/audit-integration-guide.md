# Audit Integration Guide

This guide shows how to wire the audit subsystem into `agent run` for tamper-proof logging.

## Overview

The audit system captures three types of events:
- **Console logs** - Agent stdout/stderr
- **Network requests** - HTTP requests through the proxy
- **Credential usage** - When credentials are injected

All events are hash-chained and stored in a Merkle tree for cryptographic verification.

## Integration Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         agent run                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. Create run storage                                           │
│     └── ~/.agentops/runs/<run-id>/                              │
│         ├── metadata.json                                        │
│         ├── audit.db          ← NEW: SQLite audit store         │
│         └── audit.sock        ← NEW: Unix socket (Docker)        │
│                                                                  │
│  2. Start audit collector                                        │
│     ├── Docker: Unix socket at audit.sock                        │
│     └── Apple containers: TCP with token auth                    │
│                                                                  │
│  3. Start proxy with audit callback                              │
│     └── OnRequest callback logs to collector                     │
│                                                                  │
│  4. Start container                                              │
│     ├── Mount audit.sock (Docker)                                │
│     └── Set AUDIT_ENDPOINT env var                               │
│                                                                  │
│  5. Pipe stdout to collector                                     │
│     └── Tee output to both terminal and collector                │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Step-by-Step Integration

### 1. Initialize Audit Store

Create the audit store alongside other run storage:

```go
import "github.com/andybons/agentops/internal/audit"

func (m *Manager) Create(opts Options) (*Run, error) {
    // Existing storage setup
    store, err := storage.NewRunStore(storage.DefaultBaseDir(), runID)
    if err != nil {
        return nil, err
    }

    // NEW: Create audit store
    auditPath := filepath.Join(store.Dir(), "audit.db")
    auditStore, err := audit.OpenStore(auditPath)
    if err != nil {
        return nil, fmt.Errorf("creating audit store: %w", err)
    }

    r := &Run{
        ID:         runID,
        Store:      store,
        AuditStore: auditStore,  // Add to Run struct
        // ...
    }
    return r, nil
}
```

### 2. Start the Collector

Start the collector before the container, choosing transport based on runtime:

```go
func (m *Manager) Start(r *Run) error {
    collector := audit.NewCollector(r.AuditStore)

    if m.runtime.Type() == "docker" {
        // Docker: Use Unix socket (no auth needed - socket permissions)
        socketPath := filepath.Join(r.Store.Dir(), "audit.sock")
        if err := collector.StartUnix(socketPath); err != nil {
            return fmt.Errorf("starting audit collector: %w", err)
        }
        r.AuditSocket = socketPath
    } else {
        // Apple containers: Use TCP with token auth
        token := make([]byte, 32)
        if _, err := rand.Read(token); err != nil {
            return err
        }
        r.AuditToken = hex.EncodeToString(token)

        port, err := collector.StartTCP(r.AuditToken)
        if err != nil {
            return fmt.Errorf("starting audit collector: %w", err)
        }
        r.AuditPort = port
    }

    r.Collector = collector
    // ... continue with container start
}
```

### 3. Wire Proxy to Log Network Requests

The proxy already intercepts all HTTP traffic. Add a callback to log requests:

```go
func (m *Manager) Start(r *Run) error {
    // ... after collector start

    proxyOpts := proxy.Options{
        // Existing options...

        // NEW: Audit callback
        OnRequest: func(req proxy.RequestInfo) {
            r.AuditStore.AppendNetwork(audit.NetworkData{
                Method:         req.Method,
                URL:            req.URL,
                StatusCode:     req.StatusCode,
                DurationMs:     req.Duration.Milliseconds(),
                CredentialUsed: req.CredentialUsed,
                Error:          req.Error,
            })
        },
    }

    r.ProxyServer, err = proxy.NewServer(proxyOpts)
    // ...
}
```

### 4. Log Credential Usage

Log when credentials are injected (in the proxy's injection logic):

```go
// In proxy.Server.handleRequest
func (s *Server) injectCredentials(req *http.Request) {
    for _, cred := range s.credentials {
        if cred.Matches(req.Host) {
            req.Header.Set("Authorization", "Bearer "+cred.Token)

            // NEW: Log credential usage
            if s.auditStore != nil {
                s.auditStore.AppendCredential(audit.CredentialData{
                    Name:   cred.Name,
                    Action: "injected",
                    Host:   req.Host,
                })
            }
            break
        }
    }
}
```

### 5. Mount Socket into Container

For Docker, mount the audit socket so the agent can write logs:

```go
func (m *DockerRuntime) Start(r *Run) error {
    mounts := []mount.Mount{
        // Existing workspace mount...

        // NEW: Audit socket mount (write-only)
        {
            Type:     mount.TypeBind,
            Source:   r.AuditSocket,
            Target:   "/run/audit.sock",
            ReadOnly: false, // Agent needs write access
        },
    }

    env := []string{
        // Existing env vars...
        "AUDIT_ENDPOINT=unix:///run/audit.sock",
    }

    // ...
}
```

For Apple containers, pass the TCP endpoint:

```go
func (m *AppleRuntime) Start(r *Run) error {
    gatewayIP := m.getGatewayIP()
    env := []string{
        // Existing env vars...
        fmt.Sprintf("AUDIT_ENDPOINT=tcp://%s:%s", gatewayIP, r.AuditPort),
        fmt.Sprintf("AUDIT_TOKEN=%s", r.AuditToken),
    }
    // ...
}
```

### 6. Pipe Container Output to Collector

Tee container stdout/stderr to both the terminal and the collector:

```go
func (m *Manager) Start(r *Run) error {
    // ... after container start

    // Create a writer that sends to both stdout and audit
    auditWriter := &auditLogWriter{store: r.AuditStore}
    multiWriter := io.MultiWriter(os.Stdout, auditWriter)

    go io.Copy(multiWriter, containerStdout)
    go io.Copy(multiWriter, containerStderr)
}

type auditLogWriter struct {
    store *audit.Store
}

func (w *auditLogWriter) Write(p []byte) (n int, err error) {
    // Split into lines and log each
    lines := strings.Split(string(p), "\n")
    for _, line := range lines {
        if line != "" {
            w.store.AppendConsole(line)
        }
    }
    return len(p), nil
}
```

### 7. Clean Shutdown

Stop the collector after the container exits:

```go
func (m *Manager) Stop(r *Run) error {
    // Stop container first
    if err := m.runtime.Stop(r.ContainerID); err != nil {
        log.Warn("stopping container", "error", err)
    }

    // Then stop collector (flushes pending writes)
    if r.Collector != nil {
        if err := r.Collector.Stop(); err != nil {
            log.Warn("stopping audit collector", "error", err)
        }
    }

    // Close audit store
    if r.AuditStore != nil {
        r.AuditStore.Close()
    }

    return nil
}
```

## Verification

After a run completes, verify the audit log:

```bash
# View audit log with verification status
agent audit <run-id>

# Export portable proof bundle
agent audit <run-id> --export proof.json

# Verify a proof bundle offline
agent audit --verify proof.json
```

## Security Considerations

1. **Socket permissions** - Unix socket is created with 0222 (write-only) so agents cannot read their own logs
2. **Token entropy** - TCP tokens must be at least 32 bytes from `crypto/rand`
3. **Hash chaining** - Each entry includes the previous entry's hash, making insertions detectable
4. **Merkle tree** - Root hash summarizes all entries; any modification changes the root

## Next Steps

- [ ] Add `AuditStore` field to `run.Run` struct
- [ ] Add `Collector` field to `run.Run` struct
- [ ] Implement `OnRequest` callback in proxy
- [ ] Add socket mount to Docker runtime
- [ ] Add TCP endpoint to Apple runtime
- [ ] Wire stdout/stderr to collector
- [ ] Update `agent audit` command to work with run IDs
