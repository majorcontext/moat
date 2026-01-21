package trace

// New creates a platform-appropriate tracer.
// On Linux, uses proc connector for real-time notifications.
// On macOS, uses sysctl polling.
// On other platforms, returns a stub tracer.
func New(cfg Config) (Tracer, error) {
	return newPlatformTracer(cfg)
}
