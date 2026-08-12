package claude

// AgentRuntime implementation. See provider.AgentRuntime.

func (p *OAuthProvider) DefaultDependencies() []string { return DefaultDependencies() }
func (p *OAuthProvider) NetworkHosts() []string        { return NetworkHosts() }

// CredentialGrant is static: the claude grant, regardless of what is stored.
func (p *OAuthProvider) CredentialGrant() string { return "claude" }
