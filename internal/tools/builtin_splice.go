package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// spliceResult is the JSON output of the splice tool.
type spliceResult struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Mode       string `json:"mode"`
	LinesAdded int    `json:"lines_added"`
	Line       int    `json:"line,omitempty"`
	Summary    string `json:"summary"`
}

// SpliceDef inserts file contents into a target file.
var SpliceDef = ToolDef{
	Name:        "splice",
	Description: "Splice file contents into a target file. Supports four modes: append (after last line), prepend (before first line), replace (overwrite target), and insert (at line N).",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to read content from.",
			},
			"target": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to write content to.",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"append", "prepend", "replace", "insert"},
				"description": "Operation mode.",
			},
			"line": map[string]any{
				"type":        "integer",
				"description": "Line number for insert mode. Content is inserted after this line.",
			},
		},
		"required": []string{"source", "target", "mode"},
	},
	Builtin: builtinSplice,
	Timeout: 10 * time.Second,
}

func builtinSplice(ctx context.Context, input map[string]any, workDir string) Result {
	source, ok := input["source"].(string)
	if !ok || source == "" {
		return Result{IsError: true, Error: "source is required"}
	}
	target, ok := input["target"].(string)
	if !ok || target == "" {
		return Result{IsError: true, Error: "target is required"}
	}
	mode, ok := input["mode"].(string)
	if !ok || mode == "" {
		return Result{IsError: true, Error: "mode is required (append|prepend|replace|insert)"}
	}

	source = resolvePath(source, workDir)
	target = resolvePath(target, workDir)

	lineNum := 0
	if v, exists := input["line"]; exists {
		lineNum = toInt(v)
	}

	switch mode {
	case "append", "prepend", "replace", "insert":
		// valid
	default:
		return Result{IsError: true, Error: fmt.Sprintf("invalid mode %q: must be append|prepend|replace|insert", mode)}
	}

	if mode == "insert" && lineNum < 1 {
		return Result{IsError: true, Error: "line is required for insert mode and must be >= 1"}
	}

	r, err := doSplice(source, target, mode, lineNum)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(r)
}

func doSplice(source, target, mode string, lineNum int) (*spliceResult, error) {
	sourceData, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file: %w", err)
	}

	sourceContent := string(sourceData)
	sourceLineCount := spliceCountLines(sourceContent)

	result := &spliceResult{
		Source:     source,
		Target:     target,
		Mode:       mode,
		LinesAdded: sourceLineCount,
	}

	switch mode {
	case "replace":
		err = AtomicWrite(target, sourceData)
	case "append":
		err = doSpliceAppend(target, sourceData)
	case "prepend":
		err = doSplicePrepend(target, sourceData)
	case "insert":
		result.Line = lineNum
		err = doSpliceInsert(target, sourceData, lineNum)
	}

	if err != nil {
		return nil, err
	}

	result.Summary = spliceSummary(source, target, mode, lineNum, sourceLineCount)
	return result, nil
}

func doSpliceAppend(targetPath string, sourceData []byte) error {
	targetData, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("failed to read target file: %w", err)
	}

	var combined []byte
	if len(targetData) > 0 && targetData[len(targetData)-1] != '\n' {
		combined = make([]byte, 0, len(targetData)+1+len(sourceData))
		combined = append(combined, targetData...)
		combined = append(combined, '\n')
	} else {
		combined = make([]byte, 0, len(targetData)+len(sourceData))
		combined = append(combined, targetData...)
	}
	combined = append(combined, sourceData...)

	return AtomicWrite(targetPath, combined)
}

func doSplicePrepend(targetPath string, sourceData []byte) error {
	targetData, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("failed to read target file: %w", err)
	}

	var combined []byte
	if len(sourceData) > 0 && sourceData[len(sourceData)-1] != '\n' {
		combined = make([]byte, 0, len(sourceData)+1+len(targetData))
		combined = append(combined, sourceData...)
		combined = append(combined, '\n')
	} else {
		combined = make([]byte, 0, len(sourceData)+len(targetData))
		combined = append(combined, sourceData...)
	}
	combined = append(combined, targetData...)

	return AtomicWrite(targetPath, combined)
}

func doSpliceInsert(targetPath string, sourceData []byte, lineNum int) error {
	targetData, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("failed to read target file: %w", err)
	}

	targetContent := string(targetData)
	lines := strings.Split(targetContent, "\n")

	hasTrailingNewline := len(targetContent) > 0 && targetContent[len(targetContent)-1] == '\n'
	if hasTrailingNewline && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if lineNum > len(lines) {
		return fmt.Errorf("line %d exceeds file length (%d lines)", lineNum, len(lines))
	}

	sourceStr := string(sourceData)
	if len(sourceStr) > 0 && sourceStr[len(sourceStr)-1] == '\n' {
		sourceStr = sourceStr[:len(sourceStr)-1]
	}
	sourceLines := strings.Split(sourceStr, "\n")

	newLines := make([]string, 0, len(lines)+len(sourceLines))
	newLines = append(newLines, lines[:lineNum]...)
	newLines = append(newLines, sourceLines...)
	newLines = append(newLines, lines[lineNum:]...)

	output := strings.Join(newLines, "\n")
	if hasTrailingNewline {
		output += "\n"
	}

	return AtomicWrite(targetPath, []byte(output))
}

func spliceCountLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if s[len(s)-1] != '\n' {
		n++
	}
	return n
}

func spliceSummary(source, target, mode string, lineNum, lineCount int) string {
	lineWord := "lines"
	if lineCount == 1 {
		lineWord = "line"
	}

	switch mode {
	case "append":
		return fmt.Sprintf("Appended %d %s from %s to %s", lineCount, lineWord, source, target)
	case "prepend":
		return fmt.Sprintf("Prepended %d %s from %s to %s", lineCount, lineWord, source, target)
	case "replace":
		return fmt.Sprintf("Replaced %s with %d %s from %s", target, lineCount, lineWord, source)
	case "insert":
		return fmt.Sprintf("Inserted %d %s from %s into %s at line %d", lineCount, lineWord, source, target, lineNum)
	}
	return ""
}
