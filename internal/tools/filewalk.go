// Package tools — shared utilities for builtin tool implementations.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AtomicWrite writes content to a file via temp+rename for crash safety.
// Preserves the original file's permissions if it exists.
func AtomicWrite(path string, content []byte) error {
	perm := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nostop-tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, writeErr := tmp.Write(content); writeErr != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", closeErr)
	}

	if chmodErr := os.Chmod(tmpPath, perm); chmodErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("setting permissions: %w", chmodErr)
	}

	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp to target: %w", renameErr)
	}

	return nil
}

// WalkOptions controls directory traversal for file discovery.
type WalkOptions struct {
	Dirs       []string // directories to scan
	Files      []string // specific files (bypass directory scan)
	Ext        string   // extension filter (e.g. ".go")
	Recursive  bool     // recurse into subdirectories
	SkipHidden bool     // skip dotfiles/dotdirs
}

// WalkFiles yields file paths matching the options.
// If Files is non-empty, those are returned directly (filtered by Ext).
// Otherwise, Dirs are scanned.
func WalkFiles(opts WalkOptions) ([]string, error) {
	if len(opts.Files) > 0 {
		return filterByExt(opts.Files, opts.Ext), nil
	}

	dirs := opts.Dirs
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	var result []string
	for _, dir := range dirs {
		files, err := scanDir(dir, opts.Ext, opts.Recursive, opts.SkipHidden)
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", dir, err)
		}
		result = append(result, files...)
	}

	return result, nil
}

// scanDir scans a directory for files, optionally recursing.
func scanDir(dir, ext string, recursive, skipHidden bool) ([]string, error) {
	if recursive {
		return scanDirRecursive(dir, ext, skipHidden)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if skipHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if ext != "" && !strings.HasSuffix(name, ext) {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	return files, nil
}

// scanDirRecursive walks a directory tree.
func scanDirRecursive(dir, ext string, skipHidden bool) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		name := d.Name()
		if skipHidden && strings.HasPrefix(name, ".") && d.IsDir() {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if skipHidden && strings.HasPrefix(name, ".") {
			return nil
		}
		if ext != "" && !strings.HasSuffix(name, ext) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// filterByExt filters a list of paths by extension.
func filterByExt(paths []string, ext string) []string {
	if ext == "" {
		return paths
	}
	var result []string
	for _, p := range paths {
		if strings.HasSuffix(p, ext) {
			result = append(result, p)
		}
	}
	return result
}
