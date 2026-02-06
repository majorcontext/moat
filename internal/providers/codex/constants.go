package codex

// OpenAIAPIKeyPlaceholder is a placeholder that looks like a valid OpenAI API key.
// Some tools validate the API key format locally before making requests.
// Using a valid-looking placeholder bypasses these checks while still allowing
// the proxy to inject the real key at the network layer.
const OpenAIAPIKeyPlaceholder = "sk-moat-proxy-injected-placeholder-0000000000000000000000000000000000000000"

// ProxyInjectedPlaceholder is a generic placeholder value for credentials that
// will be injected by the Moat proxy at runtime.
const ProxyInjectedPlaceholder = "moat-proxy-injected"
