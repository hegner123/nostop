package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// conflictsConflict represents a single merge conflict.
type conflictsConflict struct {
	File         string `json:"file"`
	Line         int    `json:"line"`
	EndLine      int    `json:"end_line"`
	OursRef      string `json:"ours_ref"`
	TheirsRef    string `json:"theirs_ref"`
	Ours         string `json:"ours"`
	Theirs       string `json:"theirs"`
	Base         string `json:"base,omitempty"`
	ContextAbove string `json:"context_above,omitempty"`
	ContextBelow string `json:"context_below,omitempty"`
}

// conflictsFileResult groups conflicts by file.
type conflictsFileResult struct {
	File      string              `json:"file"`
	Conflicts []conflictsConflict `json:"conflicts"`
	Count     int                 `json:"count"`
}

// conflictsResult is the top-level output.
type conflictsResult struct {
	Files    []conflictsFileResult `json:"files"`
	Total    int                   `json:"total"`
	HasDiff3 bool                  `json:"has_diff3"`
	Summary  string                `json:"summary"`
}

// Conflict marker prefixes
const (
	conflictMarkerOurs   = "<<<<<<<"
	conflictMarkerBase   = "|||||||"
	conflictMarkerSep    = "======="
	conflictMarkerTheirs = ">>>>>>>"
)

// conflictParseState tracks position within a conflict block.
type conflictParseState int

const (
	conflictStateNone conflictParseState = iota
	conflictStateOurs
	conflictStateBase
	conflictStateTheirs
)

// ConflictsDef parses git merge conflict markers.
var ConflictsDef = ToolDef{
	Name:        "conflicts",
	Description: "Parse git merge conflict markers in files. Returns structured JSON with each conflict's ours/theirs content, line numbers, refs, and surrounding context. Supports standard and diff3 styles.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of file paths to parse for conflicts.",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"default":     1,
				"description": "Number of context lines above and below each conflict.",
			},
		},
		"required": []string{"file"},
	},
	Builtin: builtinConflicts,
	Timeout: 15 * time.Second,
}

func builtinConflicts(ctx context.Context, input map[string]any, workDir string) Result {
	// Parse file param (accepts string or array)
	var files []string
	switch v := input["file"].(type) {
	case string:
		if v == "" {
			return Result{IsError: true, Error: "file is required"}
		}
		files = []string{v}
	case []any:
		for _, f := range v {
			if s, ok := f.(string); ok && s != "" {
				files = append(files, s)
			}
		}
	default:
		return Result{IsError: true, Error: "file is required"}
	}

	if len(files) == 0 {
		return Result{IsError: true, Error: "file is required"}
	}

	// Resolve paths
	for i := range files {
		files[i] = resolvePath(files[i], workDir)
	}

	contextLines := 1
	if v, ok := input["context_lines"]; ok {
		n := toInt(v)
		if n >= 0 {
			contextLines = n
		}
	}

	result, err := doParseConflicts(files, contextLines)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(result)
}

func doParseConflicts(files []string, contextLines int) (*conflictsResult, error) {
	result := &conflictsResult{
		Files: make([]conflictsFileResult, 0, len(files)),
	}

	hasDiff3 := false

	for _, filePath := range files {
		fileResult, diff3, err := parseFileConflicts(filePath, contextLines)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", filePath, err)
		}
		if diff3 {
			hasDiff3 = true
		}
		result.Files = append(result.Files, *fileResult)
		result.Total += fileResult.Count
	}

	result.HasDiff3 = hasDiff3

	fileWord := "file"
	if len(files) != 1 {
		fileWord = "files"
	}
	conflictWord := "conflict"
	if result.Total != 1 {
		conflictWord = "conflicts"
	}
	result.Summary = fmt.Sprintf("Found %d %s in %d %s", result.Total, conflictWord, len(files), fileWord)

	return result, nil
}

func parseFileConflicts(filePath string, contextLines int) (*conflictsFileResult, bool, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, false, err
	}

	lines := strings.Split(string(data), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}

	fileResult := &conflictsFileResult{
		File:      filePath,
		Conflicts: make([]conflictsConflict, 0),
	}

	hasDiff3 := false
	state := conflictStateNone
	var current conflictsConflict
	var oursLines, baseLines, theirsLines []string

	for lineNum, line := range lines {
		lineNo := lineNum + 1

		switch state {
		case conflictStateNone:
			if strings.HasPrefix(line, conflictMarkerOurs) {
				state = conflictStateOurs
				current = conflictsConflict{
					File: filePath,
					Line: lineNo,
				}
				current.OursRef = strings.TrimSpace(line[len(conflictMarkerOurs):])
				oursLines = nil
				baseLines = nil
				theirsLines = nil
			}

		case conflictStateOurs:
			if strings.HasPrefix(line, conflictMarkerBase) {
				state = conflictStateBase
				hasDiff3 = true
			} else if strings.HasPrefix(line, conflictMarkerSep) && !strings.HasPrefix(line, conflictMarkerSep+"=") {
				state = conflictStateTheirs
			} else {
				oursLines = append(oursLines, line)
			}

		case conflictStateBase:
			if strings.HasPrefix(line, conflictMarkerSep) && !strings.HasPrefix(line, conflictMarkerSep+"=") {
				state = conflictStateTheirs
			} else {
				baseLines = append(baseLines, line)
			}

		case conflictStateTheirs:
			if strings.HasPrefix(line, conflictMarkerTheirs) {
				current.EndLine = lineNo
				current.TheirsRef = strings.TrimSpace(line[len(conflictMarkerTheirs):])
				current.Ours = strings.Join(oursLines, "\n")
				current.Theirs = strings.Join(theirsLines, "\n")
				if len(baseLines) > 0 {
					current.Base = strings.Join(baseLines, "\n")
				}

				if contextLines > 0 {
					current.ContextAbove = gatherConflictContext(lines, current.Line-2, contextLines, -1)
					current.ContextBelow = gatherConflictContext(lines, current.EndLine, contextLines, 1)
				}

				fileResult.Conflicts = append(fileResult.Conflicts, current)
				state = conflictStateNone
			} else {
				theirsLines = append(theirsLines, line)
			}
		}
	}

	fileResult.Count = len(fileResult.Conflicts)
	return fileResult, hasDiff3, nil
}

func gatherConflictContext(lines []string, startIdx int, n int, direction int) string {
	collected := make([]string, 0, n)

	for i := range n {
		idx := startIdx + (i * direction)
		if idx < 0 || idx >= len(lines) {
			break
		}
		if isConflictMarkerLine(lines[idx]) {
			break
		}
		collected = append(collected, lines[idx])
	}

	if direction == -1 {
		for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
			collected[i], collected[j] = collected[j], collected[i]
		}
	}

	return strings.Join(collected, "\n")
}

func isConflictMarkerLine(line string) bool {
	return strings.HasPrefix(line, conflictMarkerOurs) ||
		strings.HasPrefix(line, conflictMarkerBase) ||
		(strings.HasPrefix(line, conflictMarkerSep) && !strings.HasPrefix(line, conflictMarkerSep+"=")) ||
		strings.HasPrefix(line, conflictMarkerTheirs)
}
