package gemini

// AgentRuntime implementation. See provider.AgentRuntime.

func (p *Provider) DefaultDependencies() []string { return DefaultDependencies() }
func (p *Provider) NetworkHosts() []string        { return NetworkHosts() }

// CredentialGrant is static, unlike the store-probing closure gemini wires into
// ProviderRunConfig. That closure returns "" when nothing is stored, which
// would put an empty grant into the grants list.
func (p *Provider) CredentialGrant() string { return "gemini" }
