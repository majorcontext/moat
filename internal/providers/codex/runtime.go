package codex

// AgentRuntime implementation. See provider.AgentRuntime.

func (p *Provider) DefaultDependencies() []string { return DefaultDependencies() }
func (p *Provider) NetworkHosts() []string        { return NetworkHosts() }

// CredentialGrant returns the logical Codex grant. The run resolver prefers
// subscription auth and falls back to an independently stored OpenAI API key.
func (p *Provider) CredentialGrant() string { return "codex" }
