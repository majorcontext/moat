package ui

import (
	"fmt"
	"io"
	"os"
)

var writer io.Writer = os.Stderr

// SetWriter overrides the output writer (for testing).
func SetWriter(w io.Writer) {
	writer = w
}

// Warn prints a user-facing warning to stderr.
func Warn(msg string) {
	fmt.Fprintf(writer, "Warning: %s\n", msg)
}

// Warnf prints a formatted user-facing warning to stderr.
func Warnf(format string, args ...any) {
	fmt.Fprintf(writer, "Warning: "+format+"\n", args...)
}

// Error prints a user-facing error to stderr.
func Error(msg string) {
	fmt.Fprintf(writer, "Error: %s\n", msg)
}

// Errorf prints a formatted user-facing error to stderr.
func Errorf(format string, args ...any) {
	fmt.Fprintf(writer, "Error: "+format+"\n", args...)
}

// Info prints a user-facing message to stderr with no prefix.
func Info(msg string) {
	fmt.Fprintf(writer, "%s\n", msg)
}

// Infof prints a formatted user-facing message to stderr with no prefix.
func Infof(format string, args ...any) {
	fmt.Fprintf(writer, format+"\n", args...)
}
