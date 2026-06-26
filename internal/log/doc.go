// Package log is moat's structured diagnostic logger (log.Debug/Info/Warn/Error).
//
// It writes JSON to ~/.moat/debug/ and only surfaces on stderr with --verbose,
// so it is for internal state, timing, and request details — anything useful
// for debugging but not for the user. For messages the user needs to see, use
// the internal/ui package instead.
package log
