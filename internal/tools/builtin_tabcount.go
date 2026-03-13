package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"
)

// tabcountLineIndent is a single line's indentation measurement.
type tabcountLineIndent struct {
	Line        int `json:"line"`
	Indentation int `json:"indentation"`
}

// tabcountResult is the JSON output of the tabcount tool.
type tabcountResult struct {
	File       string               `json:"file"`
	StartLine  int                  `json:"start_line"`
	EndLine    int                  `json:"end_line"`
	TotalLines int                  `json:"total_lines"`
	Lines      []tabcountLineIndent `json:"lines"`
}

// TabcountDef counts leading tab characters per line in a file.
var TabcountDef = ToolDef{
	Name:        "tabcount",
	Description: "Count leading tab characters per line. Returns each line number and its tab depth. Useful for verifying indentation in Go files or diagnosing edit failures due to indentation mismatch.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to analyze.",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"default":     0,
				"description": "Start line number (1-based). Defaults to beginning of file.",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"default":     0,
				"description": "End line number (1-based, inclusive). Defaults to end of file.",
			},
		},
		"required": []string{"file"},
	},
	Builtin: builtinTabcount,
	Timeout: 10 * time.Second,
}

func builtinTabcount(ctx context.Context, input map[string]any, workDir string) Result {
	filePath, ok := input["file"].(string)
	if !ok || filePath == "" {
		return Result{IsError: true, Error: "file is required"}
	}
	filePath = resolvePath(filePath, workDir)

	startLine := 1
	if v, ok := input["start_line"]; ok {
		n := toInt(v)
		if n >= 1 {
			startLine = n
		}
	}

	endLine := 0
	if v, ok := input["end_line"]; ok {
		endLine = toInt(v)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return Result{IsError: true, Error: fmt.Sprintf("opening file: %s", err)}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []tabcountLineIndent
	lineNum := 0

	for scanner.Scan() {
		lineNum++

		if lineNum < startLine {
			continue
		}
		if endLine > 0 && lineNum > endLine {
			break
		}

		line := scanner.Text()
		indent := 0
		for _, ch := range line {
			if ch != '\t' {
				break
			}
			indent++
		}

		lines = append(lines, tabcountLineIndent{
			Line:        lineNum,
			Indentation: indent,
		})
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return Result{IsError: true, Error: fmt.Sprintf("reading file: %s", scanErr)}
	}

	actualEnd := endLine
	if actualEnd == 0 || actualEnd > lineNum {
		actualEnd = lineNum
	}

	return resultJSON(tabcountResult{
		File:       filePath,
		StartLine:  startLine,
		EndLine:    actualEnd,
		TotalLines: len(lines),
		Lines:      lines,
	})
}
