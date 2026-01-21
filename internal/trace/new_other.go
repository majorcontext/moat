//go:build !linux && !darwin

package trace

func newPlatformTracer(cfg Config) (Tracer, error) {
	return NewStubTracer(cfg), nil
}
