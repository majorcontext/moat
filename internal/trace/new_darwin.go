//go:build darwin

package trace

func newPlatformTracer(cfg Config) (Tracer, error) {
	return NewDarwinTracer(cfg)
}
