// Package ui renders user-facing messages and styling for moat's CLI.
//
// ui.Warn/Error/Info always print to stderr (with colored prefixes when it is a
// TTY) and are for warnings, errors, and status the user needs to see — as
// opposed to internal/log, which is diagnostic output hidden behind --verbose.
// The styling helpers (ui.Bold, ui.Green, ui.OKTag, …) degrade to plain strings
// when stdout is not a TTY or NO_COLOR is set.
package ui
