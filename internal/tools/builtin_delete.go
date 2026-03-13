package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// deleteResult is the JSON output of the delete tool.
type deleteResult struct {
	OriginalPath string `json:"original_path"`
	TrashPath    string `json:"trash_path"`
	Type         string `json:"type"`
	Size         int64  `json:"size"`
	Items        int    `json:"items,omitempty"`
}

// blockedPrefixes are system paths that should never be trashed.
var blockedPrefixes = []string{
	"/System",
	"/Library",
	"/usr",
	"/bin",
	"/sbin",
	"/etc",
	"/var",
	"/private",
	"/Applications",
}

// DeleteDef moves files/directories to macOS Trash.
var DeleteDef = ToolDef{
	Name:        "delete",
	Description: "Move a file or directory to macOS Trash. Safe alternative to rm/rmdir. Handles name collisions. Works on files, directories, and symlinks.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file or directory to trash.",
			},
		},
		"required": []string{"path"},
	},
	Builtin: builtinDelete,
	Timeout: 10 * time.Second,
}

func builtinDelete(ctx context.Context, input map[string]any, workDir string) Result {
	path, ok := input["path"].(string)
	if !ok || path == "" {
		return Result{IsError: true, Error: "path is required"}
	}

	trashDir := filepath.Join(os.Getenv("HOME"), ".Trash")
	result, err := trashPathTo(path, trashDir)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(result)
}

// trashPathTo moves a path to the specified trash directory.
func trashPathTo(path, trashDir string) (*deleteResult, error) {
	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve path: %w", err)
	}

	// Block system paths
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(absPath, prefix) {
			return nil, fmt.Errorf("refusing to trash system path: %s", absPath)
		}
	}

	// Block trashing Trash itself
	if absPath == trashDir {
		return nil, fmt.Errorf("refusing to trash the Trash directory itself")
	}

	// Stat the path (lstat to not follow symlinks)
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("path does not exist: %s", absPath)
		}
		return nil, fmt.Errorf("cannot stat path: %w", err)
	}

	// Determine type and size
	itemType := "file"
	var size int64
	var items int

	if info.IsDir() {
		itemType = "directory"
		size, items = dirStats(absPath)
	} else if info.Mode()&os.ModeSymlink != 0 {
		itemType = "symlink"
		size = info.Size()
	} else {
		size = info.Size()
	}

	// Ensure Trash directory exists
	if mkdirErr := os.MkdirAll(trashDir, 0755); mkdirErr != nil {
		return nil, fmt.Errorf("cannot create Trash directory: %w", mkdirErr)
	}

	// Determine destination name, handle collisions
	baseName := filepath.Base(absPath)
	destPath := filepath.Join(trashDir, baseName)

	if _, statErr := os.Lstat(destPath); statErr == nil {
		// Name collision — append timestamp
		ext := filepath.Ext(baseName)
		stem := strings.TrimSuffix(baseName, ext)
		timestamp := time.Now().Format("20060102_150405")
		destPath = filepath.Join(trashDir, fmt.Sprintf("%s_%s%s", stem, timestamp, ext))

		// If still collides (sub-second), add nanoseconds
		if _, statErr2 := os.Lstat(destPath); statErr2 == nil {
			timestamp = time.Now().Format("20060102_150405.000000000")
			destPath = filepath.Join(trashDir, fmt.Sprintf("%s_%s%s", stem, timestamp, ext))
		}
	}

	// Move to Trash
	if renameErr := os.Rename(absPath, destPath); renameErr != nil {
		return nil, fmt.Errorf("failed to move to Trash: %w", renameErr)
	}

	return &deleteResult{
		OriginalPath: absPath,
		TrashPath:    destPath,
		Type:         itemType,
		Size:         size,
		Items:        items,
	}, nil
}

// dirStats calculates total size and item count for a directory.
func dirStats(path string) (totalSize int64, totalItems int) {
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors, best effort
		}
		totalItems++
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		totalSize += info.Size()
		return nil
	})
	return totalSize, totalItems
}
