// Package log provides a colored slog.Logger for console output.
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
)

var levelColors = map[slog.Level]string{
	slog.LevelDebug: colorGray,
	slog.LevelInfo:  colorGreen,
	slog.LevelWarn:  colorYellow,
	slog.LevelError: colorRed,
}

// ColoredHandler wraps slog.Handler with ANSI color output.
type ColoredHandler struct {
	handler slog.Handler
	mu      sync.Mutex
	w       io.Writer
}

// NewColoredHandler creates a handler that writes colored log lines to w.
// If w is not a terminal, colors are stripped (io.Discard check for simplicity).
func NewColoredHandler(w io.Writer, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Remove time from structured output — we add it manually
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}
	return &ColoredHandler{
		handler: slog.NewTextHandler(w, opts),
		w:       w,
	}
}

func (h *ColoredHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *ColoredHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ColoredHandler{
		handler: h.handler.WithAttrs(attrs),
		w:       h.w,
	}
}

func (h *ColoredHandler) WithGroup(name string) slog.Handler {
	return &ColoredHandler{
		handler: h.handler.WithGroup(name),
		w:       h.w,
	}
}

func (h *ColoredHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	color := levelColors[r.Level]
	levelStr := r.Level.String()
	ts := r.Time.Format("15:04:05")

	// Build the message from attrs
	attrs := make([]string, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
		return true
	})

	var attrStr string
	if len(attrs) > 0 {
		attrStr = " " + fmt.Sprintf("%s%s%s", colorCyan, strings.Join(attrs, " "), colorReset)
	}

	line := fmt.Sprintf("%s[%s] %-5s%s %s%s%s\n",
		color, ts, levelStr, colorReset,
		r.Message, attrStr, colorReset)

	_, err := fmt.Fprint(h.w, line)
	return err
}

// NewLogger creates a slog.Logger that writes colored output to stderr.
func NewLogger(level slog.Level) *slog.Logger {
	handler := NewColoredHandler(os.Stderr, level)
	return slog.New(handler)
}

// NewFileLogger creates a pair: colored stderr logger + plain file logger.
// The returned *slog.Logger writes to both (via the file handler fallback).
func NewFileLogger(level slog.Level, logDir string, retentionDays int) (*slog.Logger, *os.File, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("create log dir: %w", err)
	}

	now := time.Now()
	logPath := filepath.Join(logDir, now.Format("2006-01-02")+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}

	// File handler: no color, plain text
	fileOpts := &slog.HandlerOptions{Level: level}
	fileHandler := slog.NewTextHandler(f, fileOpts)

	// Multi-handler: colored stderr + file
	consoleHandler := NewColoredHandler(os.Stderr, level)
	multiHandler := &multiHandler{handlers: []slog.Handler{consoleHandler, fileHandler}}

	logger := slog.New(multiHandler)

	// Cleanup old logs
	if retentionDays > 0 {
		go cleanupOldLogs(logDir, retentionDays)
	}

	return logger, f, nil
}

// multiHandler fans out to multiple handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return &multiHandler{handlers: newHandlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return &multiHandler{handlers: newHandlers}
}

func cleanupOldLogs(dir string, retentionDays int) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		dateStr := entry.Name()[:len(entry.Name())-4]
		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if fileDate.Before(cutoff) {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
