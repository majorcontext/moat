package claude

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// ctxKeyBaseURL carries the --base-url flag from the CLI into Grant.
type ctxKeyBaseURLType struct{}

var ctxKeyBaseURL = ctxKeyBaseURLType{}

// WithBaseURL records the endpoint an anthropic credential authenticates
// against, for `moat grant anthropic --base-url <url>`. A credential granted
// this way is a gateway key: runs using it point ANTHROPIC_BASE_URL at the
// endpoint and inject the key for that host, so it never enters the container.
func WithBaseURL(ctx context.Context, baseURL string) context.Context {
	return context.WithValue(ctx, ctxKeyBaseURL, baseURL)
}

// BaseURLFromContext returns the endpoint set by WithBaseURL, or "" for an
// ordinary Anthropic key.
func BaseURLFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyBaseURL).(string)
	return v
}

// ValidateBaseURL checks a --base-url value before it is used to validate a key
// or written to a credential. It mirrors the moat.yaml claude.base_url rules so
// the two sources cannot disagree about what a usable endpoint is.
//
// The returned URL has any trailing slash removed, since the path is joined
// onto it later.
func ValidateBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host in %q", raw)
	}
	return strings.TrimSuffix(raw, "/"), nil
}
