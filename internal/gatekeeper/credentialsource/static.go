package credentialsource

import "context"

type staticSource struct {
	value string
}

// NewStaticSource creates a CredentialSource that returns a fixed value.
func NewStaticSource(value string) CredentialSource {
	return &staticSource{value: value}
}

func (s *staticSource) Fetch(_ context.Context) (string, error) {
	return s.value, nil
}

func (s *staticSource) Type() string { return "static" }
