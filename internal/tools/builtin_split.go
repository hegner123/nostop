package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// splitPartResult describes one output file from the split.
type splitPartResult struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	LineCount int    `json:"line_count"`
}

// splitResult is the JSON output of the split tool.
type splitResult struct {
	Summary    string            `json:"summary"`
	SourceFile string            `json:"source_file"`
	TotalLines int               `json:"total_lines"`
	Parts      []splitPartResult `json:"parts"`
}

// SplitDef splits a file into multiple parts at specified line numbers.
var SplitDef = ToolDef{
	Name:        "split",
	Description: "Split a file into multiple parts at specified line numbers. Output files are named with sequential suffixes (e.g., file_001.txt, file_002.txt).",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to split.",
			},
			"lines": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Array of line numbers to split at. Each number marks the last line of a part.",
			},
		},
		"required": []string{"file", "lines"},
	},
	Builtin: builtinSplit,
	Timeout: 30 * time.Second,
}

func builtinSplit(ctx context.Context, input map[string]any, workDir string) Result {
	filePath, ok := input["file"].(string)
	if !ok || filePath == "" {
		return Result{IsError: true, Error: "file is required"}
	}
	filePath = resolvePath(filePath, workDir)

	linesRaw, ok := input["lines"].([]any)
	if !ok || len(linesRaw) == 0 {
		return Result{IsError: true, Error: "lines is required (non-empty array of integers)"}
	}

	splitPoints := make([]int, 0, len(linesRaw))
	for _, v := range linesRaw {
		n := toInt(v)
		if n < 1 {
			return Result{IsError: true, Error: fmt.Sprintf("invalid line number: %v (must be positive)", v)}
		}
		splitPoints = append(splitPoints, n)
	}

	result, err := doSplitFile(filePath, splitPoints)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(result)
}

func doSplitFile(filePath string, splitLines []int) (*splitResult, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("cannot access file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}

	// Read all lines
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	var allLines []string
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("failed to read file: %w", scanErr)
	}

	totalLines := len(allLines)
	if totalLines == 0 {
		return nil, fmt.Errorf("file is empty: %s", filePath)
	}

	// Detect line ending
	lineEnding := detectLineEndingSplit(filePath)

	// Sort and deduplicate, filter out-of-range
	sort.Ints(splitLines)
	points := dedupAndFilterSplit(splitLines, totalLines)
	if len(points) == 0 {
		return nil, fmt.Errorf("no valid split points for file with %d lines", totalLines)
	}

	// Build ranges (0-based: [start, end) )
	type lineRange struct{ start, end int }
	var ranges []lineRange
	prev := 0
	for _, p := range points {
		ranges = append(ranges, lineRange{start: prev, end: p})
		prev = p
	}
	if prev < totalLines {
		ranges = append(ranges, lineRange{start: prev, end: totalLines})
	}

	// Output naming
	dir := filepath.Dir(filePath)
	ext := filepath.Ext(filePath)
	base := strings.TrimSuffix(filepath.Base(filePath), ext)

	width := len(fmt.Sprintf("%d", len(ranges)))
	if width < 3 {
		width = 3
	}

	result := &splitResult{
		SourceFile: filePath,
		TotalLines: totalLines,
		Parts:      make([]splitPartResult, 0, len(ranges)),
	}

	for i, r := range ranges {
		partNum := fmt.Sprintf("%0*d", width, i+1)
		outName := fmt.Sprintf("%s_%s%s", base, partNum, ext)
		outPath := filepath.Join(dir, outName)

		partLines := allLines[r.start:r.end]
		if writeErr := writeSplitLines(outPath, partLines, lineEnding); writeErr != nil {
			return nil, fmt.Errorf("failed to write %s: %w", outPath, writeErr)
		}

		result.Parts = append(result.Parts, splitPartResult{
			Path:      outPath,
			StartLine: r.start + 1,
			EndLine:   r.end,
			LineCount: r.end - r.start,
		})
	}

	partWord := "parts"
	if len(result.Parts) == 1 {
		partWord = "part"
	}
	result.Summary = fmt.Sprintf("Split %s (%d lines) into %d %s",
		filepath.Base(filePath), totalLines, len(result.Parts), partWord)

	return result, nil
}

func dedupAndFilterSplit(sorted []int, totalLines int) []int {
	var result []int
	seen := make(map[int]bool)
	for _, n := range sorted {
		if n < 1 || n >= totalLines {
			continue
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		result = append(result, n)
	}
	return result
}

func detectLineEndingSplit(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "\n"
	}
	defer f.Close()

	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	for i := 0; i < n-1; i++ {
		if buf[i] == '\r' && buf[i+1] == '\n' {
			return "\r\n"
		}
		if buf[i] == '\n' {
			return "\n"
		}
	}
	return "\n"
}

func writeSplitLines(path string, lines []string, lineEnding string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".split-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	writer := bufio.NewWriter(tmp)
	for i, line := range lines {
		if i > 0 {
			writer.WriteString(lineEnding)
		}
		writer.WriteString(line)
	}
	if len(lines) > 0 {
		writer.WriteString(lineEnding)
	}

	if flushErr := writer.Flush(); flushErr != nil {
		tmp.Close()
		return flushErr
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		tmp.Close()
		return syncErr
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return closeErr
	}
	if chmodErr := os.Chmod(tmpPath, 0644); chmodErr != nil {
		return chmodErr
	}
	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		return renameErr
	}

	success = true
	return nil
}
