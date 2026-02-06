package log

import (
	"context"
	"io"
	"log/slog"
	"os"
)

var logger *slog.Logger
var fileWriter *FileWriter

// Options configures the logger.
type Options struct {
	// Verbose enables debug/info output to stderr (non-interactive only)
	Verbose bool
	// JSONFormat uses JSON output format for stderr
	JSONFormat bool
	// Interactive mode suppresses debug/info to stderr regardless of Verbose
	Interactive bool
	// DebugDir is the directory for debug log files. If empty, file logging is disabled.
	DebugDir string
	// RetentionDays is how many days to keep log files (0 = no cleanup)
	RetentionDays int
	// Stderr is the writer for stderr output (defaults to os.Stderr)
	Stderr io.Writer
}

// Init initializes the global logger with the given options.
func Init(opts Options) error {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	var handlers []slog.Handler

	// Stderr handler: Warn+Error by default, all levels if verbose && !interactive
	stderrLevel := slog.LevelWarn
	if opts.Verbose && !opts.Interactive {
		stderrLevel = slog.LevelDebug
	}

	stderrOpts := &slog.HandlerOptions{
		Level: stderrLevel,
	}

	if opts.JSONFormat {
		handlers = append(handlers, slog.NewJSONHandler(stderr, stderrOpts))
	} else {
		handlers = append(handlers, slog.NewTextHandler(stderr, stderrOpts))
	}

	// File handler: always all levels, always JSON
	if opts.DebugDir != "" {
		// Clean up old files first
		if opts.RetentionDays > 0 {
			Cleanup(opts.DebugDir, opts.RetentionDays)
		}

		fw, err := NewFileWriter(opts.DebugDir)
		if err != nil {
			return err
		}
		fileWriter = fw

		fileOpts := &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}
		handlers = append(handlers, slog.NewJSONHandler(fileWriter, fileOpts))
	}

	logger = slog.New(&multiHandler{handlers: handlers})
	slog.SetDefault(logger)
	return nil
}

// Close closes the file writer if one was created.
func Close() {
	if fileWriter != nil {
		fileWriter.Close()
		fileWriter = nil
	}
}

// multiHandler fans out log records to multiple handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: newHandlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: newHandlers}
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

// SetRunID adds a run_id attribute to all subsequent log messages.
// Call this when a run starts to correlate all logs with the run.
func SetRunID(runID string) {
	logger = slog.New(logger.Handler().WithAttrs([]slog.Attr{
		slog.String("run_id", runID),
	}))
	slog.SetDefault(logger)
}

// ClearRunID removes the run_id attribute from subsequent log messages.
// Call this when a run ends.
func ClearRunID() {
	// Re-initialize without run_id by getting base handlers
	// For simplicity, we just set run_id to empty which will still appear
	// but signals no active run. Full removal would require re-init.
	logger = slog.New(logger.Handler().WithAttrs([]slog.Attr{
		slog.String("run_id", ""),
	}))
	slog.SetDefault(logger)
}

func init() {
	// Default logger until Init is called
	logger = slog.Default()
}
