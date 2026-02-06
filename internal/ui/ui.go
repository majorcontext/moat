package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

var writer io.Writer = os.Stderr

// SetWriter overrides the output writer (for testing).
func SetWriter(w io.Writer) {
	writer = w
}

// --- Color detection ---

var stdoutColor = detectColor(os.Stdout)
var stderrColor = detectColor(os.Stderr)

func detectColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// SetColorEnabled overrides color detection (for testing).
func SetColorEnabled(enabled bool) {
	stdoutColor = enabled
	stderrColor = enabled
}

// ColorEnabled reports whether stdout color is enabled.
func ColorEnabled() bool {
	return stdoutColor
}

// --- ANSI style functions (stdout) ---

func ansi(code, s string) string {
	if !stdoutColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func ansiStderr(code, s string) string {
	if !stderrColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// Bold returns s wrapped in bold ANSI codes (stdout).
func Bold(s string) string { return ansi("1", s) }

// Dim returns s wrapped in dim ANSI codes (stdout).
func Dim(s string) string { return ansi("2", s) }

// Green returns s wrapped in green ANSI codes (stdout).
func Green(s string) string { return ansi("32", s) }

// Red returns s wrapped in red ANSI codes (stdout).
func Red(s string) string { return ansi("31", s) }

// Yellow returns s wrapped in yellow ANSI codes (stdout).
func Yellow(s string) string { return ansi("33", s) }

// Cyan returns s wrapped in cyan ANSI codes (stdout).
func Cyan(s string) string { return ansi("36", s) }

// --- Formatting helpers ---

// Section prints a bold title with a thin underline to stdout.
func Section(title string) {
	fmt.Println(Bold(title))
	fmt.Println(Dim(strings.Repeat("─", len(title))))
}

// OKTag returns a green "✓" for success indicators.
func OKTag() string { return Green("✓") }

// FailTag returns a red "✗" for failure indicators.
func FailTag() string { return Red("✗") }

// WarnTag returns a yellow "⚠" for warning indicators.
func WarnTag() string { return Yellow("⚠") }

// InfoTag returns a cyan "ℹ" for info indicators.
func InfoTag() string { return Cyan("ℹ") }

// --- Warn / Error / Info (stderr, colored prefix) ---

// Warn prints a user-facing warning to stderr.
func Warn(msg string) {
	fmt.Fprintf(writer, "%s %s\n", ansiStderr("33", "Warning:"), msg)
}

// Warnf prints a formatted user-facing warning to stderr.
func Warnf(format string, args ...any) {
	fmt.Fprintf(writer, "%s %s\n", ansiStderr("33", "Warning:"), fmt.Sprintf(format, args...))
}

// Error prints a user-facing error to stderr.
func Error(msg string) {
	fmt.Fprintf(writer, "%s %s\n", ansiStderr("31", "Error:"), msg)
}

// Errorf prints a formatted user-facing error to stderr.
func Errorf(format string, args ...any) {
	fmt.Fprintf(writer, "%s %s\n", ansiStderr("31", "Error:"), fmt.Sprintf(format, args...))
}

// Info prints a user-facing message to stderr with no prefix.
func Info(msg string) {
	fmt.Fprintf(writer, "%s\n", msg)
}

// Infof prints a formatted user-facing message to stderr with no prefix.
func Infof(format string, args ...any) {
	fmt.Fprintf(writer, format+"\n", args...)
}
