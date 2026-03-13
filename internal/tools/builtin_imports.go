package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// importsImport is a single import statement.
type importsImport struct {
	Package string `json:"package"`
	Type    string `json:"type"` // stdlib, external, local, relative, system
	Line    int    `json:"line"`
}

// importsFileResult groups imports for a single file.
type importsFileResult struct {
	Path     string          `json:"path"`
	Language string          `json:"language"`
	Imports  []importsImport `json:"imports"`
}

// importsResult is the top-level output.
type importsResult struct {
	Dir          string              `json:"dir"`
	FilesScanned int                 `json:"files_scanned"`
	Files        []importsFileResult `json:"files,omitempty"`
	Packages     map[string][]string `json:"packages,omitempty"`
	Summary      string              `json:"summary"`
}

// Directories to skip during recursive scans.
var importsSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, ".svn": true,
	".hg": true, "__pycache__": true, ".tox": true, "dist": true,
	"build": true, ".build": true, "target": true, "zig-out": true,
	"zig-cache": true, ".zig-cache": true, ".claude": true,
	".venv": true, "venv": true, "env": true, ".mypy_cache": true,
	".pytest_cache": true, "DerivedData": true, ".gradle": true,
}

// Extension to language mapping.
var importsExtToLang = map[string]string{
	".go": "go", ".py": "python", ".pyi": "python",
	".js": "javascript", ".jsx": "javascript", ".ts": "typescript", ".tsx": "typescript",
	".mjs": "javascript", ".cjs": "javascript",
	".zig": "zig", ".rs": "rust", ".c": "c", ".h": "c",
	".cpp": "cpp", ".hpp": "cpp", ".cc": "cpp", ".hh": "cpp",
	".swift": "swift", ".java": "java", ".kt": "kotlin", ".kts": "kotlin",
	".rb": "ruby", ".sh": "shell", ".bash": "shell", ".zsh": "shell",
}

// ImportsDef maps imports and dependencies across a directory.
var ImportsDef = ToolDef{
	Name:        "imports",
	Description: "Map imports and dependencies for files in a directory. Shows which files import which packages and which local files reference each other. Supports Go, Python, JS/TS, Zig, Rust, C/C++, Swift, Java/Kotlin, Ruby, and shell scripts.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dir": map[string]any{
				"type":        "string",
				"description": "Absolute path to the directory to scan for imports.",
			},
			"ext": map[string]any{
				"type":        "string",
				"description": "Filter by file extension (e.g., '.go'). Only scan files with this extension.",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Recursively scan subdirectories.",
			},
		},
		"required": []string{"dir"},
	},
	Builtin: builtinImports,
	Timeout: 30 * time.Second,
}

func builtinImports(ctx context.Context, input map[string]any, workDir string) Result {
	dir, ok := input["dir"].(string)
	if !ok || dir == "" {
		return Result{IsError: true, Error: "dir is required"}
	}
	dir = resolvePath(dir, workDir)

	ext := ""
	if v, ok := input["ext"].(string); ok {
		ext = v
	}

	recursive := false
	if v, ok := input["recursive"].(bool); ok {
		recursive = v
	}

	r, err := doImportsScan(dir, ext, recursive)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(r)
}

func doImportsScan(dir, ext string, recursive bool) (*importsResult, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve directory: %w", err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("directory does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", absDir)
	}

	goModule := importsFindGoModule(absDir)

	result := &importsResult{
		Dir:      absDir,
		Files:    make([]importsFileResult, 0),
		Packages: make(map[string][]string),
	}

	var walk func(string) error
	walk = func(scanDir string) error {
		entries, readErr := os.ReadDir(scanDir)
		if readErr != nil {
			return fmt.Errorf("failed to read directory %s: %w", scanDir, readErr)
		}

		for _, entry := range entries {
			fullPath := filepath.Join(scanDir, entry.Name())

			if entry.IsDir() {
				if importsSkipDirs[entry.Name()] {
					continue
				}
				if recursive {
					walk(fullPath)
				}
				continue
			}

			entryInfo, infoErr := entry.Info()
			if infoErr != nil || !entryInfo.Mode().IsRegular() {
				continue
			}

			if ext != "" && filepath.Ext(entry.Name()) != ext {
				continue
			}

			lang := importsDetectLang(entry.Name())
			if lang == "" {
				continue
			}

			data, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				continue
			}

			lines := strings.Split(string(data), "\n")
			imports := importsParseFile(fullPath, lines, lang, goModule)

			result.FilesScanned++

			if len(imports) == 0 {
				continue
			}

			relPath, relErr := filepath.Rel(absDir, fullPath)
			if relErr != nil {
				relPath = fullPath
			}

			result.Files = append(result.Files, importsFileResult{
				Path:     relPath,
				Language: lang,
				Imports:  imports,
			})

			for _, imp := range imports {
				result.Packages[imp.Package] = append(result.Packages[imp.Package], relPath)
			}
		}
		return nil
	}

	if walkErr := walk(absDir); walkErr != nil {
		return nil, walkErr
	}

	// Deduplicate package entries
	for pkg, files := range result.Packages {
		seen := make(map[string]bool)
		deduped := make([]string, 0, len(files))
		for _, f := range files {
			if !seen[f] {
				seen[f] = true
				deduped = append(deduped, f)
			}
		}
		result.Packages[pkg] = deduped
	}

	// Summary
	totalImports := 0
	stdlibCount := 0
	externalCount := 0
	localCount := 0
	langCounts := make(map[string]int)

	for _, f := range result.Files {
		langCounts[f.Language]++
		for _, imp := range f.Imports {
			totalImports++
			switch imp.Type {
			case "stdlib", "system":
				stdlibCount++
			case "external":
				externalCount++
			case "local", "relative":
				localCount++
			}
		}
	}

	var langParts []string
	for lang, count := range langCounts {
		langParts = append(langParts, fmt.Sprintf("%d %s", count, lang))
	}
	sort.Strings(langParts)

	langInfo := ""
	if len(langParts) > 0 {
		langInfo = fmt.Sprintf(" (%s)", strings.Join(langParts, ", "))
	}

	result.Summary = fmt.Sprintf("Scanned %d files%s: %d imports (%d stdlib, %d external, %d local) across %d packages",
		result.FilesScanned, langInfo, totalImports, stdlibCount, externalCount, localCount, len(result.Packages))

	return result, nil
}

func importsDetectLang(path string) string {
	ext := filepath.Ext(path)
	return importsExtToLang[ext]
}

func importsFindGoModule(dir string) string {
	current := dir
	for {
		data, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil {
			lines := strings.SplitN(string(data), "\n", 2)
			if len(lines) > 0 && strings.HasPrefix(lines[0], "module ") {
				return strings.TrimSpace(lines[0][7:])
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ""
}

func importsParseFile(path string, lines []string, lang, goModule string) []importsImport {
	switch lang {
	case "go":
		return importsParseGo(lines, goModule)
	case "python":
		return importsParsePython(lines)
	case "javascript", "typescript":
		return importsParseJS(lines)
	case "zig":
		return importsParseZig(lines)
	case "rust":
		return importsParseRust(lines)
	case "c", "cpp":
		return importsParseC(lines)
	case "swift":
		return importsParseSwift(lines)
	case "java", "kotlin":
		return importsParseJava(lines)
	case "ruby":
		return importsParseRuby(lines)
	case "shell":
		return importsParseShell(lines)
	default:
		return nil
	}
}

// --- Go ---

func importsParseGo(lines []string, goModule string) []importsImport {
	var imports []importsImport
	inBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}

		if inBlock {
			if strings.HasPrefix(trimmed, ")") {
				inBlock = false
				continue
			}
			pkg := importsExtractGoPackage(trimmed)
			if pkg != "" {
				imports = append(imports, importsImport{Package: pkg, Type: importsClassifyGo(pkg, goModule), Line: i + 1})
			}
			continue
		}

		if strings.HasPrefix(trimmed, "import (") {
			rest := trimmed[8:]
			if strings.Contains(rest, ")") {
				pkg := importsExtractGoPackage(rest)
				if pkg != "" {
					imports = append(imports, importsImport{Package: pkg, Type: importsClassifyGo(pkg, goModule), Line: i + 1})
				}
			} else {
				inBlock = true
			}
			continue
		}

		if strings.HasPrefix(trimmed, "import ") {
			pkg := importsExtractGoPackage(trimmed[7:])
			if pkg != "" {
				imports = append(imports, importsImport{Package: pkg, Type: importsClassifyGo(pkg, goModule), Line: i + 1})
			}
		}
	}
	return imports
}

func importsExtractGoPackage(s string) string {
	start := strings.IndexByte(s, '"')
	if start == -1 {
		return ""
	}
	end := strings.IndexByte(s[start+1:], '"')
	if end == -1 {
		return ""
	}
	return s[start+1 : start+1+end]
}

func importsClassifyGo(pkg, goModule string) string {
	if goModule != "" && strings.HasPrefix(pkg, goModule) {
		return "local"
	}
	first := pkg
	if idx := strings.IndexByte(pkg, '/'); idx >= 0 {
		first = pkg[:idx]
	}
	if strings.ContainsRune(first, '.') {
		return "external"
	}
	return "stdlib"
}

// --- Python ---

func importsParsePython(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "from ") {
			rest := trimmed[5:]
			spaceIdx := strings.IndexByte(rest, ' ')
			if spaceIdx == -1 {
				continue
			}
			pkg := rest[:spaceIdx]
			impType := "external"
			if strings.HasPrefix(pkg, ".") {
				impType = "relative"
			}
			imports = append(imports, importsImport{Package: pkg, Type: impType, Line: i + 1})
			continue
		}
		if strings.HasPrefix(trimmed, "import ") {
			parts := strings.Split(trimmed[7:], ",")
			for _, part := range parts {
				p := strings.TrimSpace(part)
				if asIdx := strings.Index(p, " as "); asIdx >= 0 {
					p = p[:asIdx]
				}
				p = strings.TrimSpace(p)
				if p != "" {
					imports = append(imports, importsImport{Package: p, Type: "external", Line: i + 1})
				}
			}
		}
	}
	return imports
}

// --- JavaScript / TypeScript ---

func importsParseJS(lines []string) []importsImport {
	var imports []importsImport
	inMultiline := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if inMultiline {
			if strings.Contains(trimmed, " from ") {
				pkg := importsExtractJSFrom(trimmed)
				if pkg != "" {
					imports = append(imports, importsImport{Package: pkg, Type: importsClassifyJS(pkg), Line: i + 1})
				}
				inMultiline = false
			}
			continue
		}

		if strings.HasPrefix(trimmed, "import ") {
			if strings.Contains(trimmed, " from ") {
				pkg := importsExtractJSFrom(trimmed)
				if pkg != "" {
					imports = append(imports, importsImport{Package: pkg, Type: importsClassifyJS(pkg), Line: i + 1})
				}
			} else if importsContainsQuote(trimmed[7:]) {
				pkg := importsExtractQuoted(trimmed[7:])
				if pkg != "" {
					imports = append(imports, importsImport{Package: pkg, Type: importsClassifyJS(pkg), Line: i + 1})
				}
			} else {
				inMultiline = true
			}
			continue
		}

		if strings.HasPrefix(trimmed, "export ") && strings.Contains(trimmed, " from ") {
			pkg := importsExtractJSFrom(trimmed)
			if pkg != "" {
				imports = append(imports, importsImport{Package: pkg, Type: importsClassifyJS(pkg), Line: i + 1})
			}
			continue
		}

		if strings.Contains(trimmed, "require(") {
			idx := strings.Index(trimmed, "require(")
			pkg := importsExtractQuoted(trimmed[idx+8:])
			if pkg != "" {
				imports = append(imports, importsImport{Package: pkg, Type: importsClassifyJS(pkg), Line: i + 1})
			}
		}
	}
	return imports
}

func importsExtractJSFrom(line string) string {
	idx := strings.Index(line, " from ")
	if idx == -1 {
		return ""
	}
	return importsExtractQuoted(line[idx+6:])
}

func importsClassifyJS(pkg string) string {
	if strings.HasPrefix(pkg, "./") || strings.HasPrefix(pkg, "../") || strings.HasPrefix(pkg, "/") {
		return "local"
	}
	return "external"
}

// --- Zig ---

func importsParseZig(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		idx := strings.Index(trimmed, "@import(")
		if idx == -1 {
			continue
		}
		pkg := importsExtractQuoted(trimmed[idx+8:])
		if pkg != "" {
			impType := "external"
			if pkg == "std" || pkg == "builtin" {
				impType = "stdlib"
			} else if strings.HasSuffix(pkg, ".zig") {
				impType = "local"
			}
			imports = append(imports, importsImport{Package: pkg, Type: impType, Line: i + 1})
		}
	}
	return imports
}

// --- Rust ---

func importsParseRust(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		working := trimmed
		if strings.HasPrefix(working, "pub ") {
			working = working[4:]
		}

		if strings.HasPrefix(working, "use ") {
			rest := strings.TrimSuffix(working[4:], ";")
			rest = strings.TrimSpace(rest)
			if braceIdx := strings.IndexByte(rest, '{'); braceIdx >= 0 {
				rest = strings.TrimSuffix(rest[:braceIdx], "::")
			}
			if rest != "" {
				impType := "external"
				if strings.HasPrefix(rest, "std") && (rest == "std" || strings.HasPrefix(rest, "std::")) {
					impType = "stdlib"
				} else if strings.HasPrefix(rest, "core") && (rest == "core" || strings.HasPrefix(rest, "core::")) {
					impType = "stdlib"
				} else if strings.HasPrefix(rest, "alloc") && (rest == "alloc" || strings.HasPrefix(rest, "alloc::")) {
					impType = "stdlib"
				} else if strings.HasPrefix(rest, "crate::") || strings.HasPrefix(rest, "self::") || strings.HasPrefix(rest, "super::") {
					impType = "local"
				}
				imports = append(imports, importsImport{Package: rest, Type: impType, Line: i + 1})
			}
			continue
		}

		if strings.HasPrefix(working, "mod ") && strings.HasSuffix(working, ";") {
			name := strings.TrimSuffix(strings.TrimSpace(working[4:]), ";")
			if name != "" {
				imports = append(imports, importsImport{Package: name, Type: "local", Line: i + 1})
			}
			continue
		}

		if strings.HasPrefix(working, "extern crate ") {
			rest := strings.TrimSuffix(working[13:], ";")
			rest = strings.TrimSpace(rest)
			if asIdx := strings.Index(rest, " as "); asIdx >= 0 {
				rest = rest[:asIdx]
			}
			if rest != "" {
				imports = append(imports, importsImport{Package: rest, Type: "external", Line: i + 1})
			}
		}
	}
	return imports
}

// --- C / C++ ---

func importsParseC(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		directive := strings.TrimSpace(trimmed[1:])
		if !strings.HasPrefix(directive, "include") {
			continue
		}
		rest := strings.TrimSpace(directive[7:])
		if len(rest) > 0 && rest[0] == '"' {
			end := strings.IndexByte(rest[1:], '"')
			if end >= 0 {
				imports = append(imports, importsImport{Package: rest[1 : 1+end], Type: "local", Line: i + 1})
			}
		} else if len(rest) > 0 && rest[0] == '<' {
			end := strings.IndexByte(rest, '>')
			if end > 1 {
				imports = append(imports, importsImport{Package: rest[1:end], Type: "system", Line: i + 1})
			}
		}
	}
	return imports
}

// --- Swift ---

var importsSwiftStdlib = map[string]bool{
	"Swift": true, "Foundation": true, "UIKit": true, "AppKit": true,
	"SwiftUI": true, "Combine": true, "CoreData": true, "CoreGraphics": true,
	"Darwin": true, "Dispatch": true, "ObjectiveC": true, "os": true,
	"CoreFoundation": true, "CoreLocation": true, "MapKit": true,
	"AVFoundation": true, "Metal": true, "SceneKit": true, "SpriteKit": true,
	"GameplayKit": true, "ARKit": true, "RealityKit": true, "Observation": true,
}

func importsParseSwift(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if strings.HasPrefix(trimmed, "@testable ") {
			trimmed = strings.TrimSpace(trimmed[10:])
		}
		if !strings.HasPrefix(trimmed, "import ") {
			continue
		}
		rest := strings.TrimSpace(trimmed[7:])
		words := strings.Fields(rest)
		if len(words) == 0 {
			continue
		}
		module := words[0]
		kinds := []string{"class", "struct", "enum", "protocol", "func", "var", "let", "typealias"}
		for _, k := range kinds {
			if module == k && len(words) > 1 {
				module = words[1]
				break
			}
		}
		if dotIdx := strings.IndexByte(module, '.'); dotIdx >= 0 {
			module = module[:dotIdx]
		}
		impType := "external"
		if importsSwiftStdlib[module] {
			impType = "stdlib"
		}
		imports = append(imports, importsImport{Package: module, Type: impType, Line: i + 1})
	}
	return imports
}

// --- Java / Kotlin ---

func importsParseJava(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if !strings.HasPrefix(trimmed, "import ") {
			continue
		}
		rest := trimmed[7:]
		rest = strings.TrimPrefix(rest, "static ")
		rest = strings.TrimSuffix(rest, ";")
		rest = strings.TrimSpace(rest)
		if asIdx := strings.Index(rest, " as "); asIdx >= 0 {
			rest = rest[:asIdx]
		}
		if rest != "" {
			impType := "external"
			if strings.HasPrefix(rest, "java.") || strings.HasPrefix(rest, "javax.") ||
				strings.HasPrefix(rest, "kotlin.") || strings.HasPrefix(rest, "kotlinx.") {
				impType = "stdlib"
			}
			imports = append(imports, importsImport{Package: rest, Type: impType, Line: i + 1})
		}
	}
	return imports
}

// --- Ruby ---

func importsParseRuby(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "require_relative ") {
			pkg := importsExtractQuoted(trimmed[17:])
			if pkg != "" {
				imports = append(imports, importsImport{Package: pkg, Type: "local", Line: i + 1})
			}
			continue
		}
		if strings.HasPrefix(trimmed, "require ") {
			pkg := importsExtractQuoted(trimmed[8:])
			if pkg != "" {
				imports = append(imports, importsImport{Package: pkg, Type: "external", Line: i + 1})
			}
		}
	}
	return imports
}

// --- Shell ---

func importsParseShell(lines []string) []importsImport {
	var imports []importsImport
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		var file string
		if strings.HasPrefix(trimmed, "source ") {
			file = strings.TrimSpace(trimmed[7:])
		} else if strings.HasPrefix(trimmed, ". ") && len(trimmed) > 2 {
			file = strings.TrimSpace(trimmed[2:])
		}
		if file != "" {
			file = strings.Trim(file, "\"'")
			if commentIdx := strings.IndexByte(file, '#'); commentIdx >= 0 {
				file = strings.TrimSpace(file[:commentIdx])
			}
			if file != "" {
				imports = append(imports, importsImport{Package: file, Type: "local", Line: i + 1})
			}
		}
	}
	return imports
}

// --- Helpers ---

func importsExtractQuoted(s string) string {
	if idx := strings.IndexByte(s, '"'); idx >= 0 {
		end := strings.IndexByte(s[idx+1:], '"')
		if end >= 0 {
			return s[idx+1 : idx+1+end]
		}
	}
	if idx := strings.IndexByte(s, '\''); idx >= 0 {
		end := strings.IndexByte(s[idx+1:], '\'')
		if end >= 0 {
			return s[idx+1 : idx+1+end]
		}
	}
	return ""
}

func importsContainsQuote(s string) bool {
	return strings.ContainsAny(s, "\"'")
}
