package log

import (
	"context"
	"io"
	"log/slog"
	"os"
)

var logger *slog.Logger
var fileWriter *FileWriter
var verbose bool

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

	verbose = opts.Verbose

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

// Verbose returns true if verbose logging was enabled at init.
// Use this to gate sensitive debug output (tokens, credentials, raw output)
// that should only appear when the user explicitly requests verbose mode.
func Verbose() bool {
	return verbose
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

// RunContext contains run-scoped data to include in all log messages.
type RunContext struct {
	RunID     string   // Unique run identifier
	RunName   string   // Human-readable name (e.g., "my-project")
	Agent     string   // Agent type (e.g., "claude", "codex")
	Workspace string   // Project directory basename
	Image     string   // Container image used
	Grants    []string // Active credential grants
}

// SetRunContext adds run-scoped attributes to all subsequent log messages.
// Call this when a run starts to correlate all logs with the run.
func SetRunContext(ctx RunContext) {
	attrs := []slog.Attr{
		slog.String("run_id", ctx.RunID),
	}
	if ctx.RunName != "" {
		attrs = append(attrs, slog.String("run_name", ctx.RunName))
	}
	if ctx.Agent != "" {
		attrs = append(attrs, slog.String("agent", ctx.Agent))
	}
	if ctx.Workspace != "" {
		attrs = append(attrs, slog.String("workspace", ctx.Workspace))
	}
	if ctx.Image != "" {
		attrs = append(attrs, slog.String("image", ctx.Image))
	}
	if len(ctx.Grants) > 0 {
		attrs = append(attrs, slog.String("grants", joinGrants(ctx.Grants)))
	}
	logger = slog.New(logger.Handler().WithAttrs(attrs))
	slog.SetDefault(logger)
}

// joinGrants joins grant names with commas.
func joinGrants(grants []string) string {
	if len(grants) == 0 {
		return ""
	}
	result := grants[0]
	for i := 1; i < len(grants); i++ {
		result += "," + grants[i]
	}
	return result
}

// ClearRunContext removes run-scoped attributes from subsequent log messages.
// Call this when a run ends.
func ClearRunContext() {
	// Re-initialize without run context by getting base handlers
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
