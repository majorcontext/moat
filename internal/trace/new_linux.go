//go:build linux

package trace

func newPlatformTracer(cfg Config) (Tracer, error) {
	return NewProcConnectorTracer(cfg)
}
