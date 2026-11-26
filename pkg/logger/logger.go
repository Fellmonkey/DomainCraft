// Package logger provides the CLI's four-level console logger.
package logger

import (
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/term"
)

// Logger is a simple CLI logger with colored output for key stages, warnings, and errors.
type Logger struct {
	writer    io.Writer
	useColors bool
	mu        sync.Mutex
}

// New creates a Logger that writes to stderr. Colors are enabled only when
// stderr is a real terminal and NO_COLOR is unset or empty, so redirected
// diagnostics stay free of ANSI escapes. Diagnostics must never share stdout
// with machine-readable command output.
func New() *Logger {
	return NewWithWriter(os.Stderr)
}

// NewWithWriter creates a Logger for an explicit destination. Colors are
// decided from that writer, not from the process' stderr: a buffer or a pipe
// must never inherit terminal coloring just because the process was started in
// a terminal. Non-*os.File writers are treated as non-terminals.
func NewWithWriter(w io.Writer) *Logger {
	if w == nil {
		w = os.Stderr
	}
	colors := false
	if f, ok := w.(*os.File); ok {
		colors = term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == ""
	}
	return &Logger{writer: w, useColors: colors}
}

// SetColors enables or disables ANSI colors for subsequent log calls.
func (l *Logger) SetColors(enabled bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.useColors = enabled
}

// SetWriter overrides the output destination (useful for testing).
func (l *Logger) SetWriter(w io.Writer) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writer = w
}

// write emits one line while holding the lock, so concurrent calls cannot
// interleave their bytes. The level glyph and its color are fixed per level.
func (l *Logger) write(glyph, color, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.useColors {
		_, _ = fmt.Fprintf(l.writer, "  \033[%sm%s\033[0m %s\n", color, glyph, msg)
	} else {
		_, _ = fmt.Fprintf(l.writer, "  %s %s\n", glyph, msg)
	}
}

// Info logs a key stage message (e.g. "Validating schema...").
// Safe to call on a nil Logger (no-op), so renderers can accept an optional logger.
func (l *Logger) Info(format string, args ...any) {
	if l == nil {
		return
	}
	l.write("▸", "36", fmt.Sprintf(format, args...))
}

// Success logs a completed stage (e.g. "✓ Generated 12 files").
// Safe to call on a nil Logger (no-op).
func (l *Logger) Success(format string, args ...any) {
	if l == nil {
		return
	}
	l.write("✓", "32", fmt.Sprintf(format, args...))
}

// Warn logs a non-fatal warning.
// Safe to call on a nil Logger (no-op).
func (l *Logger) Warn(format string, args ...any) {
	if l == nil {
		return
	}
	l.write("⚠", "33", fmt.Sprintf(format, args...))
}

// Error logs an error message (does not exit).
// Safe to call on a nil Logger (no-op).
func (l *Logger) Error(format string, args ...any) {
	if l == nil {
		return
	}
	l.write("✗", "31", fmt.Sprintf(format, args...))
}
