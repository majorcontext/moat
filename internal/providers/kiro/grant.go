package kiro

import (
	"context"
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

// Grant is implemented in Task 4.
type Grant struct{}

func NewGrant() *Grant { return &Grant{} }

func (g *Grant) Execute(ctx context.Context) (*provider.Credential, error) {
	return nil, errors.New("not implemented")
}
