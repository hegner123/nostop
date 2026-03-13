package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stumpEntry is a single tree entry.
type stumpEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "f" for file, "d" for directory
	Size int64  `json:"size,omitempty"`
}

// stumpStats holds traversal statistics.
type stumpStats struct {
	Dirs     int `json:"dirs"`
	Files    int `json:"files"`
	Filtered int `json:"filtered"`
	Symlinks int `json:"symlinks"`
}

// stumpResult is the JSON output of the stump tool.
type stumpResult struct {
	Root  string       `json:"root"`
	Depth int          `json:"depth"`
	Stats stumpStats   `json:"stats"`
	Tree  []stumpEntry `json:"tree"`
}

// StumpDef produces token-efficient directory tree visualizations.
var StumpDef = ToolDef{
	Name:        "stump",
	Description: "Token-efficient directory tree visualization. Shows directory structure with optional depth limits, extension filtering, size display, and hidden file inclusion.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dir": map[string]any{
				"type":        "string",
				"description": "Root directory to scan.",
			},
			"depth": map[string]any{
				"type":        "integer",
				"description": "Max traversal depth (-1 for unlimited).",
			},
			"include_ext": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only include files with these extensions.",
			},
			"exclude_ext": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exclude files with these extensions.",
			},
			"exclude_patterns": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exclude paths matching these patterns.",
			},
			"show_size": map[string]any{
				"type":        "boolean",
				"description": "Include file sizes.",
			},
			"show_hidden": map[string]any{
				"type":        "boolean",
				"description": "Include hidden files and directories.",
			},
		},
		"required": []string{"dir"},
	},
	Builtin: builtinStump,
	Timeout: 15 * time.Second,
}

type stumpConfig struct {
	dir             string
	maxDepth        int
	includeExt      map[string]bool
	excludeExt      map[string]bool
	excludePatterns []string
	showSize        bool
	showHidden      bool
}

func builtinStump(ctx context.Context, input map[string]any, workDir string) Result {
	dir, ok := input["dir"].(string)
	if !ok || dir == "" {
		return Result{IsError: true, Error: "dir is required"}
	}
	dir = resolvePath(dir, workDir)

	cfg := stumpConfig{
		dir:      dir,
		maxDepth: -1, // unlimited by default
	}

	if v, ok := input["depth"]; ok {
		cfg.maxDepth = toInt(v)
	}
	if v, ok := input["include_ext"].([]any); ok {
		cfg.includeExt = make(map[string]bool)
		for _, ext := range v {
			if s, ok := ext.(string); ok {
				if !strings.HasPrefix(s, ".") {
					s = "." + s
				}
				cfg.includeExt[s] = true
			}
		}
	}
	if v, ok := input["exclude_ext"].([]any); ok {
		cfg.excludeExt = make(map[string]bool)
		for _, ext := range v {
			if s, ok := ext.(string); ok {
				if !strings.HasPrefix(s, ".") {
					s = "." + s
				}
				cfg.excludeExt[s] = true
			}
		}
	}
	if v, ok := input["exclude_patterns"].([]any); ok {
		for _, p := range v {
			if s, ok := p.(string); ok {
				cfg.excludePatterns = append(cfg.excludePatterns, s)
			}
		}
	}
	if v, ok := input["show_size"].(bool); ok {
		cfg.showSize = v
	}
	if v, ok := input["show_hidden"].(bool); ok {
		cfg.showHidden = v
	}

	result, err := doStump(cfg)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(result)
}

func doStump(cfg stumpConfig) (*stumpResult, error) {
	info, err := os.Stat(cfg.dir)
	if err != nil {
		return nil, fmt.Errorf("cannot access directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", cfg.dir)
	}

	result := &stumpResult{
		Root:  cfg.dir,
		Depth: cfg.maxDepth,
		Tree:  make([]stumpEntry, 0),
	}

	walkStump(cfg.dir, cfg.dir, 0, cfg, result)

	return result, nil
}

func walkStump(root, current string, depth int, cfg stumpConfig, result *stumpResult) {
	if cfg.maxDepth >= 0 && depth > cfg.maxDepth {
		return
	}

	entries, err := os.ReadDir(current)
	if err != nil {
		return // skip unreadable directories
	}

	for _, entry := range entries {
		name := entry.Name()

		// Hidden file/dir check
		if !cfg.showHidden && strings.HasPrefix(name, ".") {
			result.Stats.Filtered++
			continue
		}

		fullPath := filepath.Join(current, name)
		relPath, _ := filepath.Rel(root, fullPath)

		// Check exclude patterns
		if matchesExcludePattern(relPath, name, cfg.excludePatterns) {
			result.Stats.Filtered++
			if entry.IsDir() {
				result.Stats.Filtered++ // count skipped dir contents roughly
			}
			continue
		}

		if entry.Type()&os.ModeSymlink != 0 {
			result.Stats.Symlinks++
		}

		if entry.IsDir() {
			result.Stats.Dirs++
			result.Tree = append(result.Tree, stumpEntry{
				Path: relPath,
				Type: "d",
			})
			walkStump(root, fullPath, depth+1, cfg, result)
			continue
		}

		// File filtering
		ext := filepath.Ext(name)
		if len(cfg.includeExt) > 0 && !cfg.includeExt[ext] {
			result.Stats.Filtered++
			continue
		}
		if len(cfg.excludeExt) > 0 && cfg.excludeExt[ext] {
			result.Stats.Filtered++
			continue
		}

		result.Stats.Files++
		entry := stumpEntry{
			Path: relPath,
			Type: "f",
		}

		if cfg.showSize {
			if info, infoErr := os.Lstat(fullPath); infoErr == nil {
				entry.Size = info.Size()
			}
		}

		result.Tree = append(result.Tree, entry)
	}
}

func matchesExcludePattern(relPath, name string, patterns []string) bool {
	for _, pattern := range patterns {
		if name == pattern {
			return true
		}
		if strings.Contains(relPath, pattern) {
			return true
		}
	}
	return false
}
