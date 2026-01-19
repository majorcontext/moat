package sshagent

import (
	"errors"
	"testing"
)

func TestProxyFilterIdentities(t *testing.T) {
	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "allowed@example.com"},
			{KeyBlob: []byte("key2"), Comment: "blocked@example.com"},
			{KeyBlob: []byte("key3"), Comment: "allowed2@example.com"},
		},
	}

	proxy := NewProxy(upstream)

	// Allow key1 for github.com
	proxy.AllowKey(Fingerprint([]byte("key1")), []string{"github.com"})
	// Allow key3 for gitlab.com
	proxy.AllowKey(Fingerprint([]byte("key3")), []string{"gitlab.com"})

	// List should only return allowed keys
	ids, err := proxy.List()
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("List returned %d identities, want 2", len(ids))
	}
}

func TestProxyFilterAll(t *testing.T) {
	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "test@example.com"},
		},
	}

	proxy := NewProxy(upstream)
	// No keys allowed

	ids, err := proxy.List()
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("List returned %d identities, want 0 (all filtered)", len(ids))
	}
}

func TestProxySignAllowed(t *testing.T) {
	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "test@example.com"},
		},
	}

	proxy := NewProxy(upstream)
	proxy.AllowKey(Fingerprint([]byte("key1")), []string{"github.com"})
	proxy.SetCurrentHost("github.com")

	key := &Identity{KeyBlob: []byte("key1")}
	sig, err := proxy.Sign(key, []byte("data"))
	if err != nil {
		t.Fatalf("Sign error: %v", err)
	}
	if sig == nil {
		t.Error("Sign should return signature")
	}
}

func TestProxySignDeniedWrongHost(t *testing.T) {
	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "test@example.com"},
		},
	}

	proxy := NewProxy(upstream)
	proxy.AllowKey(Fingerprint([]byte("key1")), []string{"github.com"})
	proxy.SetCurrentHost("gitlab.com") // Different host!

	key := &Identity{KeyBlob: []byte("key1")}
	_, err := proxy.Sign(key, []byte("data"))
	if err == nil {
		t.Error("Sign should fail for non-granted host")
	}
}

func TestProxySignDeniedUnknownKey(t *testing.T) {
	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "test@example.com"},
		},
	}

	proxy := NewProxy(upstream)
	// key1 not allowed at all

	key := &Identity{KeyBlob: []byte("key1")}
	_, err := proxy.Sign(key, []byte("data"))
	if err == nil {
		t.Error("Sign should fail for unknown key")
	}
}

func TestProxySignSingleHostFallback(t *testing.T) {
	// When a key maps to exactly one host and no current host is set,
	// signing should be allowed (fallback for non-git SSH)
	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "test@example.com"},
		},
	}

	proxy := NewProxy(upstream)
	proxy.AllowKey(Fingerprint([]byte("key1")), []string{"github.com"})
	// No SetCurrentHost called

	key := &Identity{KeyBlob: []byte("key1")}
	sig, err := proxy.Sign(key, []byte("data"))
	if err != nil {
		t.Fatalf("Sign should succeed with single-host fallback: %v", err)
	}
	if sig == nil {
		t.Error("Sign should return signature")
	}
}

func TestProxySignMultiHostNoFallback(t *testing.T) {
	// When a key maps to multiple hosts and no current host is set,
	// signing should fail (ambiguous)
	upstream := &mockAgent{
		identities: []*Identity{
			{KeyBlob: []byte("key1"), Comment: "test@example.com"},
		},
	}

	proxy := NewProxy(upstream)
	proxy.AllowKey(Fingerprint([]byte("key1")), []string{"github.com", "gitlab.com"})
	// No SetCurrentHost called

	key := &Identity{KeyBlob: []byte("key1")}
	_, err := proxy.Sign(key, []byte("data"))
	if err == nil {
		t.Error("Sign should fail when key maps to multiple hosts and no host set")
	}
}

func TestProxyUpstreamError(t *testing.T) {
	upstream := &mockAgent{
		signErr: errors.New("upstream error"),
	}

	proxy := NewProxy(upstream)
	proxy.AllowKey(Fingerprint([]byte("key1")), []string{"github.com"})
	proxy.SetCurrentHost("github.com")

	key := &Identity{KeyBlob: []byte("key1")}
	_, err := proxy.Sign(key, []byte("data"))
	if err == nil {
		t.Error("Sign should propagate upstream error")
	}
}
