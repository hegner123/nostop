// Package tui provides debug logging for the TUI application.
package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger provides file-based debug logging for TUI applications.
type Logger struct {
	mu      sync.Mutex
	file    *os.File
	enabled bool
}

var (
	debugLog  *Logger
	debugOnce sync.Once
)

// InitLogger initializes the debug logger.
// If path is empty, uses ~/.local/share/nostop/debug.log
func InitLogger(path string, enabled bool) error {
	var initErr error
	debugOnce.Do(func() {
		debugLog = &Logger{enabled: enabled}
		if !enabled {
			return
		}

		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				initErr = err
				return
			}
			path = filepath.Join(home, ".local", "share", "nostop", "debug.log")
		}

		// Ensure directory exists
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			initErr = err
			return
		}

		// Open log file (append mode)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			initErr = err
			return
		}
		debugLog.file = f

		// Write session header
		debugLog.writeHeader()
	})
	return initErr
}

// writeHeader writes a session start marker.
func (l *Logger) writeHeader() {
	if l.file == nil {
		return
	}
	header := fmt.Sprintf("\n%s\n=== nostop Debug Session Started at %s ===\n%s\n",
		"════════════════════════════════════════════════════════════",
		time.Now().Format("2006-01-02 15:04:05"),
		"════════════════════════════════════════════════════════════")
	l.file.WriteString(header)
}

// Close closes the logger.
func CloseLogger() {
	if debugLog != nil && debugLog.file != nil {
		debugLog.file.Close()
	}
}

// Log writes a debug message with timestamp.
func Log(format string, args ...any) {
	if debugLog == nil || !debugLog.enabled || debugLog.file == nil {
		return
	}

	debugLog.mu.Lock()
	defer debugLog.mu.Unlock()

	timestamp := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(debugLog.file, "[%s] %s\n", timestamp, msg)
	debugLog.file.Sync() // Ensure it's written immediately
}

// LogMsg logs a Bubbletea message type.
func LogMsg(prefix string, msg any) {
	Log("%s: %T = %+v", prefix, msg, msg)
}

// LogError logs an error with context.
func LogError(context string, err error) {
	if err != nil {
		Log("ERROR [%s]: %v", context, err)
	}
}

// LogWriter returns an io.Writer that writes to the debug log.
// Useful for redirecting other loggers.
func LogWriter() io.Writer {
	if debugLog == nil || !debugLog.enabled || debugLog.file == nil {
		return io.Discard
	}
	return debugLog.file
}

// IsEnabled returns true if debug logging is enabled.
func IsEnabled() bool {
	return debugLog != nil && debugLog.enabled
}
