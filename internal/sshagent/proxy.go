package sshagent

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// AuditEvent represents an auditable SSH agent operation.
type AuditEvent struct {
	Action      string // "list", "sign_allowed", "sign_denied"
	Host        string // target host (for sign operations)
	Fingerprint string // key fingerprint (for sign operations)
	Error       string // error message (for denied operations)
}

// AuditFunc is a callback for audit logging.
type AuditFunc func(event AuditEvent)

// Proxy is a filtering SSH agent proxy that only exposes keys
// for granted hosts.
type Proxy struct {
	upstream    AgentClient
	allowedKeys map[string][]string // fingerprint -> allowed hosts
	currentHost atomic.Value        // string - target host for current operation
	auditFunc   AuditFunc           // optional audit callback
	mu          sync.RWMutex
}

// NewProxy creates a new filtering SSH agent proxy.
func NewProxy(upstream AgentClient) *Proxy {
	p := &Proxy{
		upstream:    upstream,
		allowedKeys: make(map[string][]string),
	}
	p.currentHost.Store("")
	return p
}

// AllowKey permits a key (by fingerprint) for specific hosts.
func (p *Proxy) AllowKey(fingerprint string, hosts []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allowedKeys[fingerprint] = hosts
}

// SetAuditFunc sets the audit callback function.
func (p *Proxy) SetAuditFunc(fn AuditFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.auditFunc = fn
}

// audit logs an event if an audit function is set.
func (p *Proxy) audit(event AuditEvent) {
	p.mu.RLock()
	fn := p.auditFunc
	p.mu.RUnlock()
	if fn != nil {
		fn(event)
	}
}

// SetCurrentHost sets the target host for sign request validation.
// This is called by the SSH wrapper to indicate which host is being connected to.
func (p *Proxy) SetCurrentHost(host string) {
	p.currentHost.Store(host)
}

// GetCurrentHost returns the current target host.
func (p *Proxy) GetCurrentHost() string {
	if v, ok := p.currentHost.Load().(string); ok {
		return v
	}
	return ""
}

// List returns only the identities that are allowed by the proxy.
func (p *Proxy) List() ([]*Identity, error) {
	all, err := p.upstream.List()
	if err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var allowed []*Identity
	for _, id := range all {
		fp := id.Fingerprint()
		if _, ok := p.allowedKeys[fp]; ok {
			allowed = append(allowed, id)
		}
	}

	p.audit(AuditEvent{Action: "list"})
	return allowed, nil
}

// Sign forwards a sign request if the key is allowed for the current host.
func (p *Proxy) Sign(key *Identity, data []byte) ([]byte, error) {
	fp := key.Fingerprint()
	host, _ := p.currentHost.Load().(string)

	p.mu.RLock()
	hosts, ok := p.allowedKeys[fp]
	p.mu.RUnlock()

	if !ok {
		errMsg := fmt.Sprintf("key %s not in allowed list", fp)
		p.audit(AuditEvent{
			Action:      "sign_denied",
			Host:        host,
			Fingerprint: fp,
			Error:       errMsg,
		})
		return nil, fmt.Errorf("key %s not in allowed list", fp)
	}

	// Check if key is allowed for this host
	allowed := false
	for _, h := range hosts {
		if h == host {
			allowed = true
			break
		}
	}

	// Fallback: if key maps to exactly one host and no host is set,
	// allow signing (for non-git SSH where host tracking may not work)
	if !allowed && host == "" && len(hosts) == 1 {
		allowed = true
		host = hosts[0] // use the single allowed host for audit
	}

	if !allowed {
		var errMsg string
		if host == "" {
			errMsg = fmt.Sprintf("key %s maps to multiple hosts; cannot determine target", fp)
			p.audit(AuditEvent{
				Action:      "sign_denied",
				Host:        host,
				Fingerprint: fp,
				Error:       errMsg,
			})
			return nil, fmt.Errorf("key %s maps to multiple hosts; cannot determine target", fp)
		}
		errMsg = fmt.Sprintf("key %s not allowed for host %s", fp, host)
		p.audit(AuditEvent{
			Action:      "sign_denied",
			Host:        host,
			Fingerprint: fp,
			Error:       errMsg,
		})
		return nil, fmt.Errorf("key %s not allowed for host %s", fp, host)
	}

	sig, err := p.upstream.Sign(key, data)
	if err != nil {
		return nil, err
	}

	p.audit(AuditEvent{
		Action:      "sign_allowed",
		Host:        host,
		Fingerprint: fp,
	})
	return sig, nil
}

// Close closes the upstream connection.
func (p *Proxy) Close() error {
	return p.upstream.Close()
}
