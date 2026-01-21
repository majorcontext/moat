// Package trace provides execution tracing for containerized processes.
//
// # Platform Support
//
// Linux: Uses the proc connector (netlink) for real-time exec notifications.
// Requires CAP_NET_ADMIN or root privileges.
//
// macOS: Uses sysctl polling to detect new processes. This is a fallback
// since the Endpoint Security Framework (ESF) requires Apple entitlements.
// Polling interval is 100ms by default.
//
// Other platforms: Uses a stub tracer that emits no events.
//
// # Usage
//
//	tracer, err := trace.New(trace.Config{PID: containerPID})
//	if err != nil {
//	    return err
//	}
//
//	tracer.OnExec(func(e trace.ExecEvent) {
//	    if e.IsGitCommit() {
//	        // Handle git commit
//	    }
//	})
//
//	if err := tracer.Start(); err != nil {
//	    return err
//	}
//	defer tracer.Stop()
//
// # Filtering
//
// Set Config.PID to only trace a specific process and its children.
// Set Config.CgroupPath (Linux only) to trace all processes in a cgroup.
package trace
