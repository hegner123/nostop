package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BuiltinTools returns definitions for all in-process builtin tools.
// These execute directly in the Go process — no external binary required.
func BuiltinTools() []ToolDef {
	return []ToolDef{
		// Core builtins
		ReadDef,
		WriteDef,
		BashDef,
		// Phase 1: trivial tools
		TabcountDef,
		NotabDef,
		DeleteDef,
		// Phase 2: file operations
		SplitDef,
		SpliceDef,
		StumpDef,
		// Phase 3: search & parse
		CheckforDef,
		ConflictsDef,
		// Phase 4: replace & diff
		RepforDef,
		CleanDiffDef,
		// Phase 5: encoding & imports
		UTF8Def,
		ImportsDef,
		// Phase 6: complex tools
		SigDef,
		TransformDef,
		ErrsDef,
	}
}

// ReadDef reads file contents with optional line offset and limit.
var ReadDef = ToolDef{
	Name:        "read",
	Description: "Read the contents of a file. Returns the file content with line numbers. Supports reading specific line ranges with offset and limit parameters for large files.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute or relative path to the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Line number to start reading from (1-based). Defaults to 1.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read. Defaults to 2000.",
			},
		},
		"required": []string{"file_path"},
	},
	Builtin: builtinRead,
	Timeout: 10 * time.Second,
}

// WriteDef writes content to a file, creating parent directories as needed.
var WriteDef = ToolDef{
	Name:        "write",
	Description: "Write content to a file. Creates the file if it doesn't exist, or overwrites it if it does. Parent directories are created automatically.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute or relative path to the file to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file.",
			},
		},
		"required": []string{"file_path", "content"},
	},
	Builtin: builtinWrite,
	Timeout: 10 * time.Second,
}

// BashDef executes a shell command and returns stdout/stderr.
var BashDef = ToolDef{
	Name:        "bash",
	Description: "Execute a bash command and return its output. The command runs in the configured working directory. Use for running builds, tests, git commands, or any shell operation.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds. Defaults to 120.",
			},
		},
		"required": []string{"command"},
	},
	Builtin: builtinBash,
	Timeout: 2 * time.Minute,
}

// --- Builtin implementations ---

// builtinRead reads a file and returns its contents with line numbers.
// After a successful read, the file's mtime is recorded in the ReadTracker
// so that builtinWrite can detect stale writes.
func builtinRead(ctx context.Context, input map[string]any, workDir string) Result {
	filePath, ok := input["file_path"].(string)
	if !ok || filePath == "" {
		return Result{IsError: true, Error: "file_path is required"}
	}

	// Resolve relative paths against workDir
	filePath = resolvePath(filePath, workDir)

	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Result{IsError: true, Error: fmt.Sprintf("failed to read file: %s", err)}
	}

	// Record this read in the tracker (mtime snapshot for stale-write detection)
	if rt := ReadTrackerFromContext(ctx); rt != nil {
		// Best-effort: don't fail the read if stat fails after successful ReadFile
		rt.RecordRead(filePath)
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	// Parse offset (1-based)
	offset := 1
	if v, ok := input["offset"]; ok {
		offset = toInt(v)
	}
	if offset < 1 {
		offset = 1
	}

	// Parse limit
	limit := 2000
	if v, ok := input["limit"]; ok {
		n := toInt(v)
		if n > 0 {
			limit = n
		}
	}

	// Slice to requested range
	startIdx := offset - 1 // convert to 0-based
	if startIdx >= totalLines {
		return resultJSON(map[string]any{
			"file":        filePath,
			"total_lines": totalLines,
			"content":     "",
			"note":        fmt.Sprintf("offset %d is beyond end of file (%d lines)", offset, totalLines),
		})
	}

	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	// Format with line numbers (cat -n style)
	var b strings.Builder
	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		// Truncate very long lines
		if len(line) > 2000 {
			line = line[:2000] + "... (truncated)"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}

	return resultJSON(map[string]any{
		"file":        filePath,
		"total_lines": totalLines,
		"from_line":   offset,
		"to_line":     endIdx,
		"content":     b.String(),
	})
}

// builtinWrite writes content to a file, creating parent directories as needed.
// If the file already exists, it must have been read first (via builtinRead) and
// must not have been modified since that read. This prevents blind overwrites.
func builtinWrite(ctx context.Context, input map[string]any, workDir string) Result {
	filePath, ok := input["file_path"].(string)
	if !ok || filePath == "" {
		return Result{IsError: true, Error: "file_path is required"}
	}

	content, ok := input["content"].(string)
	if !ok {
		return Result{IsError: true, Error: "content is required"}
	}

	// Resolve relative paths against workDir
	filePath = resolvePath(filePath, workDir)

	// Enforce read-before-write: existing files must have been read first,
	// and must not have been modified since the last read.
	if rt := ReadTrackerFromContext(ctx); rt != nil {
		if err := rt.CheckWrite(filePath); err != nil {
			return Result{IsError: true, Error: err.Error()}
		}
	}

	// Create parent directories
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return Result{IsError: true, Error: fmt.Sprintf("failed to create directories: %s", err)}
	}

	// Write the file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return Result{IsError: true, Error: fmt.Sprintf("failed to write file: %s", err)}
	}

	// Record the write so subsequent writes don't require another read
	// (the write itself constitutes knowledge of the file's current state).
	if rt := ReadTrackerFromContext(ctx); rt != nil {
		rt.RecordWrite(filePath)
	}

	return resultJSON(map[string]any{
		"file":          filePath,
		"bytes_written": len(content),
		"status":        "ok",
	})
}

// builtinBash executes a shell command via /bin/bash (or /bin/zsh if available).
func builtinBash(ctx context.Context, input map[string]any, workDir string) Result {
	command, ok := input["command"].(string)
	if !ok || command == "" {
		return Result{IsError: true, Error: "command is required"}
	}

	// Determine timeout — use the input override if provided, otherwise
	// the context already has the ToolDef timeout applied by executeBuiltin.
	if v, ok := input["timeout"]; ok {
		if secs := toInt(v); secs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(secs)*time.Second)
			defer cancel()
		}
	}

	// Prefer zsh (user's shell per CLAUDE.md), fall back to bash
	shell := "/bin/zsh"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/bash"
	}

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Check for timeout
	if ctx.Err() == context.DeadlineExceeded {
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("command timed out: %s", command),
		}
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	exitCode := 0
	if runErr != nil {
		// Extract exit code if possible
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{
				IsError: true,
				Error:   fmt.Sprintf("failed to execute command: %s", runErr),
			}
		}
	}

	// Non-zero exit is an error, but we still return all output
	if exitCode != 0 {
		output := stderrStr
		if output == "" {
			output = stdoutStr
		}
		if stdoutStr != "" && stderrStr != "" {
			output = stderrStr + "\n" + stdoutStr
		}
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("exit code %d\n%s", exitCode, output),
		}
	}

	return resultJSON(map[string]any{
		"stdout":    stdoutStr,
		"stderr":    stderrStr,
		"exit_code": exitCode,
	})
}

// --- Helpers ---

// resolvePath resolves a path relative to workDir if it's not absolute.
func resolvePath(path, workDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if workDir != "" {
		return filepath.Join(workDir, path)
	}
	return path
}

// resultJSON marshals a value to a JSON Result.
func resultJSON(v any) Result {
	data, err := json.Marshal(v)
	if err != nil {
		return Result{IsError: true, Error: fmt.Sprintf("failed to marshal result: %s", err)}
	}
	return Result{Output: string(data)}
}
