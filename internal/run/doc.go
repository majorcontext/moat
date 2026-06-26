// Package run owns the lifecycle of a moat run — a sealed, ephemeral execution
// of an agent inside an isolated container with its workspace, dependencies,
// credentials, and observability wired in.
//
// # Lifecycle
//
// A run moves through a small state machine, driven by [Manager]:
//
//		Create ──▶ Start ──▶ (running) ──▶ exit ──▶ Stopped
//		   │                                   ▲
//		   └────────────── Destroy ◀───────────┘
//
//	  - [Manager.Create] builds everything the run needs (image, mounts,
//	    credentials, proxy registration, provider staging) and returns a [Run]
//	    in the Created state. It is the heaviest phase; on any error it rolls
//	    back every resource it had acquired so far.
//	  - [Manager.Start] (or [Manager.StartAttached] for an interactive TTY)
//	    starts the container and hands control to the exit monitor.
//	  - [Manager.Wait] blocks until the run exits; [Manager.Stop] requests an
//	    early stop; [Manager.Destroy] removes the run and its resources.
//
// # Concurrency model
//
// The lifecycle is built around a single invariant that callers can rely on:
//
//   - monitorContainerExit is the only goroutine that calls WaitContainer for
//     a run, and the only one that closes the run's exit channel. Final state
//     (State, Error, StoppedAt) is written and logs are captured *before* the
//     exit channel is closed, so every Wait/Stop observer sees a consistent,
//     final view. No other path races on the container's exit.
//   - The Manager separates its general lifecycle context from the monitor
//     context so [Manager.Close] can cancel blocking WaitContainer calls with a
//     bounded timeout instead of deadlocking on a slow runtime.
//   - stateMu guards the mutable run fields that the monitor, provider
//     stopped-hooks, and user-facing methods all touch (State, Error,
//     StartedAt, StoppedAt, ProviderMeta).
//   - Exactly-once work at shutdown (resource cleanup, log capture, provider
//     stopped-hooks) is gated by CompareAndSwap atomics — sync.Once semantics,
//     but able to retry if the first attempt fails.
//
// These guarantees are what make the run package safe to drive concurrently
// from the CLI, the daemon, and the exit monitor at the same time.
package run
