package trace

// Tracer captures command executions inside a container.
type Tracer interface {
	// Start begins tracing.
	Start() error

	// Stop ends tracing.
	Stop() error

	// Events returns a channel of execution events.
	Events() <-chan ExecEvent

	// OnExec registers a callback for execution events.
	OnExec(func(ExecEvent))
}

// Config configures the tracer.
type Config struct {
	CgroupPath string // Linux: cgroup path for the container
	PID        int    // Process ID to trace (and children)
}
