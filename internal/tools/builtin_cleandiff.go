package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// cleanDiffSummary holds file-level statistics.
type cleanDiffSummary struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

// cleanDiffHunk is a single diff hunk.
type cleanDiffHunk struct {
	OldStart int      `json:"old_start"`
	OldCount int      `json:"old_count"`
	NewStart int      `json:"new_start"`
	NewCount int      `json:"new_count"`
	Header   string   `json:"header,omitempty"`
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Context  []string `json:"context,omitempty"`
}

// cleanDiffFile is a single file's diff.
type cleanDiffFile struct {
	Path       string          `json:"path"`
	OldPath    string          `json:"old_path,omitempty"`
	Status     string          `json:"status"`
	Insertions int             `json:"insertions"`
	Deletions  int             `json:"deletions"`
	Hunks      []cleanDiffHunk `json:"hunks,omitempty"`
}

// cleanDiffResult is the top-level output.
type cleanDiffResult struct {
	Summary cleanDiffSummary `json:"summary"`
	Files   []cleanDiffFile  `json:"files"`
}

// CleanDiffDef produces compact git diffs as structured JSON.
// Note: this tool inherently depends on git being installed.
var CleanDiffDef = ToolDef{
	Name:        "cleanDiff",
	Description: "Compact git diff as structured JSON. Strips context padding by default, outputs only changed lines grouped by file. Cuts diff token cost by 60-80% compared to raw diff output.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to git repository. Defaults to current directory.",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Git ref or range (e.g. 'HEAD~1', 'main..feature'). Empty for unstaged changes.",
			},
			"staged": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Show staged changes only (git diff --cached).",
			},
			"stat_only": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Only return file-level stats, omit line content.",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"default":     0,
				"description": "Number of context lines around changes.",
			},
			"file_filter": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of file paths to restrict the diff to.",
			},
		},
	},
	Builtin: builtinCleanDiff,
	Timeout: 30 * time.Second,
}

func builtinCleanDiff(ctx context.Context, input map[string]any, workDir string) Result {
	repoPath := workDir
	if v, ok := input["path"].(string); ok && v != "" {
		repoPath = resolvePath(v, workDir)
	}

	ref := ""
	if v, ok := input["ref"].(string); ok {
		ref = v
	}

	staged := false
	if v, ok := input["staged"].(bool); ok {
		staged = v
	}

	statOnly := false
	if v, ok := input["stat_only"].(bool); ok {
		statOnly = v
	}

	contextLines := 0
	if v, exists := input["context_lines"]; exists {
		contextLines = toInt(v)
	}

	var fileFilter []string
	if v, ok := input["file_filter"].([]any); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				fileFilter = append(fileFilter, s)
			}
		}
	}

	// Build git diff command
	args := []string{"diff", fmt.Sprintf("-U%d", contextLines)}
	if staged {
		args = append(args, "--cached")
	}
	if ref != "" {
		args = append(args, ref)
	}
	if len(fileFilter) > 0 {
		args = append(args, "--")
		args = append(args, fileFilter...)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return Result{IsError: true, Error: fmt.Sprintf("git diff failed: %s", string(exitErr.Stderr))}
		}
		return Result{IsError: true, Error: fmt.Sprintf("git diff failed: %s", err)}
	}

	rawDiff := string(output)
	if rawDiff == "" {
		return resultJSON(cleanDiffResult{
			Summary: cleanDiffSummary{},
			Files:   []cleanDiffFile{},
		})
	}

	files := parseUnifiedDiff(rawDiff, statOnly)

	totalInsertions := 0
	totalDeletions := 0
	for _, f := range files {
		totalInsertions += f.Insertions
		totalDeletions += f.Deletions
	}

	return resultJSON(cleanDiffResult{
		Summary: cleanDiffSummary{
			FilesChanged: len(files),
			Insertions:   totalInsertions,
			Deletions:    totalDeletions,
		},
		Files: files,
	})
}

// parseUnifiedDiff parses unified diff output into structured file diffs.
func parseUnifiedDiff(raw string, statOnly bool) []cleanDiffFile {
	var files []cleanDiffFile
	lines := strings.Split(raw, "\n")

	i := 0
	for i < len(lines) {
		line := lines[i]

		if !strings.HasPrefix(line, "diff --git ") {
			i++
			continue
		}

		file := cleanDiffFile{
			Status: "modified",
			Hunks:  []cleanDiffHunk{},
		}

		file.Path = parseDiffGitPath(line)
		i++

		// Parse metadata lines
		for i < len(lines) && !strings.HasPrefix(lines[i], "diff --git ") && !strings.HasPrefix(lines[i], "@@") && !strings.HasPrefix(lines[i], "--- ") {
			meta := lines[i]
			if strings.HasPrefix(meta, "new file mode") {
				file.Status = "added"
			} else if strings.HasPrefix(meta, "deleted file mode") {
				file.Status = "deleted"
			} else if strings.HasPrefix(meta, "rename from ") {
				file.Status = "renamed"
				file.OldPath = strings.TrimPrefix(meta, "rename from ")
			} else if strings.HasPrefix(meta, "rename to ") {
				file.Path = strings.TrimPrefix(meta, "rename to ")
			} else if strings.HasPrefix(meta, "copy from ") {
				file.Status = "copied"
				file.OldPath = strings.TrimPrefix(meta, "copy from ")
			} else if strings.HasPrefix(meta, "copy to ") {
				file.Path = strings.TrimPrefix(meta, "copy to ")
			}
			i++
		}

		// Skip --- and +++ lines
		if i < len(lines) && strings.HasPrefix(lines[i], "--- ") {
			i++
		}
		if i < len(lines) && strings.HasPrefix(lines[i], "+++ ") {
			i++
		}

		// Parse hunks
		for i < len(lines) && !strings.HasPrefix(lines[i], "diff --git ") {
			if !strings.HasPrefix(lines[i], "@@") {
				i++
				continue
			}

			hunk, nextIdx := parseDiffHunk(lines, i)
			file.Insertions += len(hunk.Added)
			file.Deletions += len(hunk.Removed)

			if !statOnly {
				file.Hunks = append(file.Hunks, hunk)
			}
			i = nextIdx
		}

		if statOnly {
			file.Hunks = nil
		}

		files = append(files, file)
	}

	return files
}

func parseDiffGitPath(line string) string {
	parts := strings.SplitN(line, " b/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	trimmed := strings.TrimPrefix(line, "diff --git a/")
	spaceIdx := strings.Index(trimmed, " ")
	if spaceIdx >= 0 {
		return trimmed[:spaceIdx]
	}
	return trimmed
}

func parseDiffHunk(lines []string, startIdx int) (cleanDiffHunk, int) {
	hunk := cleanDiffHunk{}
	line := lines[startIdx]

	hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount, hunk.Header = parseDiffHunkHeader(line)

	i := startIdx + 1
	for i < len(lines) {
		l := lines[i]
		if strings.HasPrefix(l, "diff --git ") || strings.HasPrefix(l, "@@") {
			break
		}

		if strings.HasPrefix(l, "+") {
			hunk.Added = append(hunk.Added, l[1:])
		} else if strings.HasPrefix(l, "-") {
			hunk.Removed = append(hunk.Removed, l[1:])
		} else if strings.HasPrefix(l, " ") {
			hunk.Context = append(hunk.Context, l[1:])
		}
		// Skip "\ No newline at end of file"

		i++
	}

	return hunk, i
}

func parseDiffHunkHeader(line string) (oldStart, oldCount, newStart, newCount int, header string) {
	rest := strings.TrimPrefix(line, "@@ ")
	closingIdx := strings.Index(rest, " @@")
	if closingIdx == -1 {
		return
	}

	rangeStr := rest[:closingIdx]
	header = strings.TrimSpace(rest[closingIdx+3:])

	parts := strings.Fields(rangeStr)
	if len(parts) >= 1 {
		oldStart, oldCount = parseDiffRange(parts[0])
	}
	if len(parts) >= 2 {
		newStart, newCount = parseDiffRange(parts[1])
	}

	return
}

func parseDiffRange(s string) (int, int) {
	s = strings.TrimLeft(s, "-+")
	parts := strings.SplitN(s, ",", 2)
	start, _ := strconv.Atoi(parts[0])
	count := 1
	if len(parts) == 2 {
		count, _ = strconv.Atoi(parts[1])
	}
	return start, count
}
