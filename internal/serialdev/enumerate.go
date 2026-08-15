package serialdev

import "context"

// Enumerator lists serial devices attached to the host.
//
// It is an interface so tests can supply a fixed device list: every automated
// test in this area runs without hardware.
type Enumerator interface {
	List(ctx context.Context) ([]Device, error)
}

// EnumeratorFunc adapts a function to the Enumerator interface.
type EnumeratorFunc func(ctx context.Context) ([]Device, error)

// List implements Enumerator.
func (f EnumeratorFunc) List(ctx context.Context) ([]Device, error) { return f(ctx) }
