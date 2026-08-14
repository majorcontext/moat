package copilot

// AgentRuntime implementation. See provider.AgentRuntime.

func (p *Provider) DefaultDependencies() []string { return DefaultDependencies() }
func (p *Provider) NetworkHosts() []string        { return NetworkHosts() }

// CredentialGrant is "github": copilot rides the GitHub credential
// (credentialStoreKey maps copilot -> ProviderGitHub).
func (p *Provider) CredentialGrant() string { return "github" }
