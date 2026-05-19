package kiro

import (
	"context"
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer is implemented in Task 5.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	return nil, errors.New("not implemented")
}
