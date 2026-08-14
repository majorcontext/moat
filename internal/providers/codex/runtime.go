package codex

// AgentRuntime implementation. See provider.AgentRuntime.

func (p *Provider) DefaultDependencies() []string { return DefaultDependencies() }
func (p *Provider) NetworkHosts() []string        { return NetworkHosts() }

// CredentialGrant is "openai", not "codex": the provider registry name is
// codex, but the credential is stored under openai (credential.ProviderOpenAI).
// GetCredentialName returns whichever key happens to exist, which is the wrong
// question here.
func (p *Provider) CredentialGrant() string { return "openai" }
