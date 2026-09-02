package claude

import (
	"context"
	"strings"

	"github.com/majorcontext/moat/internal/config"
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
// or written to a credential. The endpoint rules live in config.ValidateHTTPURL,
// shared with moat.yaml's claude.base_url, so the two sources cannot disagree
// about what a usable endpoint is.
//
// The returned URL has any trailing slash removed, since the path is joined
// onto it later.
func ValidateBaseURL(raw string) (string, error) {
	if _, err := config.ValidateHTTPURL(raw); err != nil {
		return "", err
	}
	return strings.TrimSuffix(raw, "/"), nil
}
