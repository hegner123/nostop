package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// notabResult is the JSON output of the notab tool.
type notabResult struct {
	File          string `json:"file"`
	Replacements  int    `json:"replacements"`
	LinesAffected int    `json:"lines_affected"`
	Direction     string `json:"direction"`
}

// NotabDef normalizes whitespace (tabs to spaces or vice versa).
var NotabDef = ToolDef{
	Name:        "notab",
	Description: "Normalize whitespace in a file. Default: replaces tabs with spaces. Inverse mode (tabs=true): replaces leading spaces with tabs, for files like Makefiles that require tab indentation.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to normalize.",
			},
			"spaces": map[string]any{
				"type":        "integer",
				"default":     4,
				"description": "Number of spaces per tab (default: 4).",
			},
			"tabs": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Inverse mode: convert leading spaces to tabs.",
			},
		},
		"required": []string{"file"},
	},
	Builtin: builtinNotab,
	Timeout: 10 * time.Second,
}

func builtinNotab(ctx context.Context, input map[string]any, workDir string) Result {
	filePath, ok := input["file"].(string)
	if !ok || filePath == "" {
		return Result{IsError: true, Error: "file is required"}
	}
	filePath = resolvePath(filePath, workDir)

	spaces := 4
	if v, ok := input["spaces"]; ok {
		n := toInt(v)
		if n >= 1 {
			spaces = n
		}
	}

	tabsMode := false
	if v, ok := input["tabs"].(bool); ok {
		tabsMode = v
	}

	if tabsMode {
		return tabifyFile(filePath, spaces)
	}
	return normalizeFile(filePath, spaces)
}

// normalizeFile replaces all tabs with spaces.
func normalizeFile(filePath string, spaces int) Result {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Result{IsError: true, Error: fmt.Sprintf("reading file: %s", err)}
	}

	replacement := strings.Repeat(" ", spaces)
	replacements := 0
	linesAffected := 0

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		count := strings.Count(line, "\t")
		if count > 0 {
			replacements += count
			linesAffected++
			lines[i] = strings.ReplaceAll(line, "\t", replacement)
		}
	}

	if replacements > 0 {
		if writeErr := AtomicWrite(filePath, []byte(strings.Join(lines, "\n"))); writeErr != nil {
			return Result{IsError: true, Error: fmt.Sprintf("writing file: %s", writeErr)}
		}
	}

	return resultJSON(notabResult{
		File:          filePath,
		Replacements:  replacements,
		LinesAffected: linesAffected,
		Direction:     "tabs_to_spaces",
	})
}

// tabifyFile converts leading spaces to tabs.
func tabifyFile(filePath string, spaces int) Result {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Result{IsError: true, Error: fmt.Sprintf("reading file: %s", err)}
	}

	spaceGroup := strings.Repeat(" ", spaces)
	replacements := 0
	linesAffected := 0

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		// Count leading space groups
		leading := 0
		for leading+spaces <= len(line) && line[leading:leading+spaces] == spaceGroup {
			leading += spaces
		}
		groupCount := leading / spaces
		if groupCount == 0 {
			continue
		}
		replacements += groupCount
		linesAffected++
		lines[i] = strings.Repeat("\t", groupCount) + line[leading:]
	}

	if replacements > 0 {
		if writeErr := AtomicWrite(filePath, []byte(strings.Join(lines, "\n"))); writeErr != nil {
			return Result{IsError: true, Error: fmt.Sprintf("writing file: %s", writeErr)}
		}
	}

	return resultJSON(notabResult{
		File:          filePath,
		Replacements:  replacements,
		LinesAffected: linesAffected,
		Direction:     "spaces_to_tabs",
	})
}
