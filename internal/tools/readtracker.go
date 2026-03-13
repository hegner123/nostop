package tools

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// ReadTracker records the mtime of files at the moment they were read.
// Before a write, the tracker checks whether the file has been modified
// since the last read — preventing blind overwrites of changed files.
//
// Rules:
//   - New files (don't exist on disk) can be written without a prior read.
//   - Existing files must be read before writing.
//   - If a file was modified after the last read, the write is rejected
//     until the file is re-read.
type ReadTracker struct {
	mu    sync.RWMutex
	reads map[string]time.Time // resolved path -> mtime at read time
}

// NewReadTracker creates an empty tracker.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		reads: make(map[string]time.Time),
	}
}

// RecordRead stores the file's current mtime as the last-read timestamp.
// Call this after a successful file read.
func (rt *ReadTracker) RecordRead(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat failed for %s: %w", path, err)
	}

	rt.mu.Lock()
	rt.reads[path] = info.ModTime()
	rt.mu.Unlock()
	return nil
}

// CheckWrite validates that a write to path is safe.
// Returns nil if the write is allowed, or an error describing why it's blocked.
//
// A write is allowed when:
//   - The file does not exist (new file creation).
//   - The file was previously read and has not been modified since.
//
// A write is blocked when:
//   - The file exists but was never read.
//   - The file exists and was modified after the last read.
func (rt *ReadTracker) CheckWrite(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // new file — write allowed
		}
		return fmt.Errorf("stat failed for %s: %w", path, err)
	}

	rt.mu.RLock()
	readMtime, wasRead := rt.reads[path]
	rt.mu.RUnlock()

	if !wasRead {
		return fmt.Errorf("file %q must be read before writing (file exists but was never read)", path)
	}

	currentMtime := info.ModTime()
	if !currentMtime.Equal(readMtime) {
		return fmt.Errorf(
			"file %q was modified after last read (read mtime: %s, current mtime: %s) — read the file again before writing",
			path,
			readMtime.Format(time.RFC3339Nano),
			currentMtime.Format(time.RFC3339Nano),
		)
	}

	return nil
}

// RecordWrite updates the tracker after a successful write so that
// subsequent writes to the same file don't require another read
// (the write itself constitutes knowledge of the file's state).
func (rt *ReadTracker) RecordWrite(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat failed for %s: %w", path, err)
	}

	rt.mu.Lock()
	rt.reads[path] = info.ModTime()
	rt.mu.Unlock()
	return nil
}

// WasRead returns true if the file has been read at least once.
func (rt *ReadTracker) WasRead(path string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	_, ok := rt.reads[path]
	return ok
}

// Clear removes all read records.
func (rt *ReadTracker) Clear() {
	rt.mu.Lock()
	rt.reads = make(map[string]time.Time)
	rt.mu.Unlock()
}

// Len returns the number of tracked files.
func (rt *ReadTracker) Len() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.reads)
}

// --- Context integration ---

type readTrackerCtxKey struct{}

// ContextWithReadTracker returns a new context carrying the ReadTracker.
func ContextWithReadTracker(ctx context.Context, rt *ReadTracker) context.Context {
	return context.WithValue(ctx, readTrackerCtxKey{}, rt)
}

// ReadTrackerFromContext extracts the ReadTracker from context, or nil if absent.
func ReadTrackerFromContext(ctx context.Context) *ReadTracker {
	rt, _ := ctx.Value(readTrackerCtxKey{}).(*ReadTracker)
	return rt
}
