package log

import (
	"io"
	"log/slog"
	"os"
)

var logger *slog.Logger

// Init initializes the global logger.
func Init(verbose bool, jsonFormat bool) {
	var handler slog.Handler

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	if jsonFormat {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	logger = slog.New(handler)
	slog.SetDefault(logger)
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	logger.Debug(msg, args...)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	logger.Info(msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	logger.Warn(msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	logger.Error(msg, args...)
}

// With returns a logger with additional context.
func With(args ...any) *slog.Logger {
	return logger.With(args...)
}

// SetOutput sets the output writer (for testing).
func SetOutput(w io.Writer) {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger = slog.New(handler)
	slog.SetDefault(logger)
}

func init() {
	// Default logger until Init is called
	logger = slog.Default()
}
