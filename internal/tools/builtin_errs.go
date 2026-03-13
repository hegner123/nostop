package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// errsParsedError is a single parsed error.
type errsParsedError struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col,omitempty"`
	Code     string `json:"code,omitempty"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message"`
}

// errsResult is the top-level output.
type errsResult struct {
	Errors  []errsParsedError `json:"errors"`
	Format  string            `json:"format"`
	Count   int               `json:"count"`
	Files   int               `json:"files"`
	Summary string            `json:"summary"`
}

// ErrsDef parses build errors and lint output.
var ErrsDef = ToolDef{
	Name:        "errs",
	Description: "Compact error/lint parser. Normalizes verbose compiler/linter output into structured JSON. Strips ANSI codes, deduplicates. Supports Go, GCC/Clang, Rust, TypeScript, ESLint, dotnet/C#, Python, Kotlin.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type":        "string",
				"description": "Raw error/lint text to parse.",
			},
			"format": map[string]any{
				"type":        "string",
				"description": "Format hint: go, eslint, tsc, rust, gcc, dotnet. Auto-detects if omitted.",
			},
		},
		"required": []string{"input"},
	},
	Builtin: builtinErrs,
	Timeout: 15 * time.Second,
}

func builtinErrs(ctx context.Context, input map[string]any, workDir string) Result {
	rawInput, ok := input["input"].(string)
	if !ok || rawInput == "" {
		return Result{IsError: true, Error: "input is required"}
	}

	formatHint := ""
	if v, ok := input["format"].(string); ok {
		formatHint = v
	}

	// Strip ANSI codes
	cleaned := errsStripANSI(rawInput)
	lines := strings.Split(cleaned, "\n")

	// Detect or use hint
	format := formatHint
	if format == "" {
		format = errsDetectFormat(lines)
	}

	var result *errsResult
	switch format {
	case "rust":
		result = errsParseRust(lines)
	case "tsc":
		result = errsParseTSC(lines)
	case "dotnet":
		result = errsParseDotnet(lines)
	case "eslint":
		result = errsParseESLint(lines)
	default:
		result = errsParseColon(lines)
	}

	return resultJSON(result)
}

// --- Format detection ---

func errsDetectFormat(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "error[") || strings.HasPrefix(trimmed, "warning[") {
			return "rust"
		}
		if strings.HasPrefix(trimmed, "--> ") {
			return "rust"
		}
	}

	for _, line := range lines {
		if errsHasDotnetProject(strings.TrimSpace(line)) {
			return "dotnet"
		}
	}

	for _, line := range lines {
		if line == "" {
			continue
		}
		parenIdx := strings.Index(line, "(")
		if parenIdx < 1 {
			continue
		}
		closeIdx := strings.Index(line[parenIdx:], ")")
		if closeIdx < 1 {
			continue
		}
		inner := line[parenIdx+1 : parenIdx+closeIdx]
		parts := strings.SplitN(inner, ",", 2)
		if len(parts) != 2 {
			continue
		}
		_, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		_, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			continue
		}
		after := line[parenIdx+closeIdx+1:]
		if strings.HasPrefix(after, ": error TS") || strings.HasPrefix(after, ": warning TS") {
			return "tsc"
		}
		if errsIsDotnetCode(after) {
			return "dotnet"
		}
	}

	if errsDetectESLint(lines) {
		return "eslint"
	}

	return "colon"
}

func errsDetectESLint(lines []string) bool {
	hasHeader := false
	for _, line := range lines {
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !hasHeader && line == trimmed {
			if (strings.Contains(line, "/") || strings.Contains(line, "\\")) && !errsHasColonDigit(line) {
				hasHeader = true
				continue
			}
		}
		if hasHeader && line != trimmed {
			fields := strings.Fields(trimmed)
			if len(fields) >= 4 {
				locParts := strings.SplitN(fields[0], ":", 2)
				if len(locParts) == 2 {
					_, e1 := strconv.Atoi(locParts[0])
					_, e2 := strconv.Atoi(locParts[1])
					if e1 == nil && e2 == nil && (fields[1] == "error" || fields[1] == "warning") {
						return true
					}
				}
			}
		}
	}
	return false
}

// --- Colon parser (Go, GCC, Clang, golangci-lint, flake8, Swift, Kotlin) ---

func errsParseColon(lines []string) *errsResult {
	var errors []errsParsedError
	seen := make(map[string]bool)

	for _, line := range lines {
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		preSeverity := ""
		if strings.HasPrefix(trimmed, "e: ") {
			preSeverity = "error"
			trimmed = trimmed[3:]
		} else if strings.HasPrefix(trimmed, "w: ") {
			preSeverity = "warning"
			trimmed = trimmed[3:]
		}

		parsed, ok := errsParseColonLine(trimmed)
		if !ok {
			continue
		}
		if parsed.Severity == "" && preSeverity != "" {
			parsed.Severity = preSeverity
		}

		key := fmt.Sprintf("%s:%d:%s", parsed.File, parsed.Line, parsed.Message)
		if seen[key] {
			continue
		}
		seen[key] = true
		errors = append(errors, parsed)
	}

	return errsBuildResult(errors, "colon")
}

func errsParseColonLine(line string) (errsParsedError, bool) {
	startSearch := 0
	if len(line) > 2 && errsIsLetter(line[0]) && line[1] == ':' && (line[2] == '\\' || line[2] == '/') {
		startSearch = 2
	}

	colonIdx := -1
	for i := startSearch; i < len(line)-1; i++ {
		if line[i] == ':' && errsIsDigit(line[i+1]) {
			colonIdx = i
			break
		}
	}
	if colonIdx < 1 {
		return errsParsedError{}, false
	}

	file := line[:colonIdx]
	rest := line[colonIdx+1:]

	lineNum, consumed := errsParseNumber(rest)
	if lineNum == 0 || consumed == 0 {
		return errsParsedError{}, false
	}
	rest = rest[consumed:]

	col := 0
	if len(rest) > 1 && rest[0] == ':' && errsIsDigit(rest[1]) {
		rest = rest[1:]
		col, consumed = errsParseNumber(rest)
		rest = rest[consumed:]
	}

	if len(rest) >= 2 && rest[0] == ':' && rest[1] == ' ' {
		rest = rest[2:]
	} else if len(rest) >= 1 && rest[0] == ':' {
		rest = rest[1:]
	} else {
		return errsParsedError{}, false
	}

	message := strings.TrimSpace(rest)
	if message == "" {
		return errsParsedError{}, false
	}

	severity := ""
	for _, sev := range []string{"fatal error", "error", "warning", "note"} {
		prefix := sev + ": "
		if strings.HasPrefix(message, prefix) {
			severity = sev
			message = message[len(prefix):]
			break
		}
	}

	code := ""
	// Trailing linter name in parens
	if lastParen := strings.LastIndex(message, " ("); lastParen > 0 && strings.HasSuffix(message, ")") {
		linterName := message[lastParen+2 : len(message)-1]
		if !strings.Contains(linterName, " ") && len(linterName) > 0 {
			code = linterName
			message = strings.TrimSpace(message[:lastParen])
		}
	}

	// Code prefix with colon
	if colonPos := strings.Index(message, ": "); colonPos > 0 && colonPos < 20 {
		potentialCode := message[:colonPos]
		if errsLooksLikeCode(potentialCode) {
			code = potentialCode
			message = message[colonPos+2:]
		}
	}

	// Code prefix with space (flake8)
	if code == "" {
		if spacePos := strings.Index(message, " "); spacePos > 0 && spacePos < 15 {
			potentialCode := message[:spacePos]
			if errsLooksLikeCode(potentialCode) {
				code = potentialCode
				message = message[spacePos+1:]
			}
		}
	}

	// Trailing code in brackets (mypy)
	if code == "" {
		if lastBracket := strings.LastIndex(message, " ["); lastBracket > 0 && strings.HasSuffix(message, "]") {
			bracketCode := message[lastBracket+2 : len(message)-1]
			if !strings.Contains(bracketCode, " ") && len(bracketCode) > 0 {
				code = bracketCode
				message = strings.TrimSpace(message[:lastBracket])
			}
		}
	}

	return errsParsedError{
		File: file, Line: lineNum, Col: col,
		Code: code, Severity: severity, Message: message,
	}, true
}

// --- Rust parser ---

func errsParseRust(lines []string) *errsResult {
	var errors []errsParsedError
	seen := make(map[string]bool)
	var curSev, curCode, curMsg string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "error[") || strings.HasPrefix(trimmed, "warning[") {
			bracketOpen := strings.Index(trimmed, "[")
			bracketClose := strings.Index(trimmed, "]")
			colonIdx := strings.Index(trimmed, "]: ")
			if bracketOpen >= 0 && bracketClose > bracketOpen && colonIdx > 0 {
				if strings.HasPrefix(trimmed, "error") {
					curSev = "error"
				} else {
					curSev = "warning"
				}
				curCode = trimmed[bracketOpen+1 : bracketClose]
				curMsg = trimmed[colonIdx+3:]
			}
			continue
		}

		if strings.HasPrefix(trimmed, "error: ") {
			curSev = "error"
			curCode = ""
			curMsg = trimmed[7:]
			continue
		}
		if strings.HasPrefix(trimmed, "warning: ") {
			curSev = "warning"
			curCode = ""
			curMsg = trimmed[9:]
			continue
		}

		if strings.HasPrefix(trimmed, "--> ") && curMsg != "" {
			location := trimmed[4:]
			file, lineNum, col := errsParseLocation(location)
			if file != "" && lineNum > 0 {
				key := fmt.Sprintf("%s:%d:%s", file, lineNum, curMsg)
				if !seen[key] {
					seen[key] = true
					errors = append(errors, errsParsedError{
						File: file, Line: lineNum, Col: col,
						Code: curCode, Severity: curSev, Message: curMsg,
					})
				}
			}
			curSev = ""
			curCode = ""
			curMsg = ""
		}
	}

	return errsBuildResult(errors, "rust")
}

// --- TypeScript parser ---

func errsParseTSC(lines []string) *errsResult {
	var errors []errsParsedError
	seen := make(map[string]bool)

	for _, line := range lines {
		if line == "" {
			continue
		}
		parenIdx := strings.Index(line, "(")
		if parenIdx < 1 {
			continue
		}
		closeIdx := strings.Index(line[parenIdx:], ")")
		if closeIdx < 1 {
			continue
		}

		file := line[:parenIdx]
		inner := line[parenIdx+1 : parenIdx+closeIdx]
		parts := strings.SplitN(inner, ",", 2)
		if len(parts) != 2 {
			continue
		}

		lineNum, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		col, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			continue
		}

		rest := line[parenIdx+closeIdx+1:]
		if !strings.HasPrefix(rest, ": ") {
			continue
		}
		rest = rest[2:]

		severity := ""
		if strings.HasPrefix(rest, "error ") {
			severity = "error"
			rest = rest[6:]
		} else if strings.HasPrefix(rest, "warning ") {
			severity = "warning"
			rest = rest[8:]
		}

		code := ""
		message := rest
		if colonPos := strings.Index(rest, ": "); colonPos > 0 && colonPos < 15 {
			potentialCode := rest[:colonPos]
			if strings.HasPrefix(potentialCode, "TS") {
				code = potentialCode
				message = rest[colonPos+2:]
			}
		}

		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}

		key := fmt.Sprintf("%s:%d:%s", file, lineNum, message)
		if seen[key] {
			continue
		}
		seen[key] = true

		errors = append(errors, errsParsedError{
			File: file, Line: lineNum, Col: col,
			Code: code, Severity: severity, Message: message,
		})
	}

	return errsBuildResult(errors, "tsc")
}

// --- Dotnet parser ---

func errsParseDotnet(lines []string) *errsResult {
	var errors []errsParsedError
	seen := make(map[string]bool)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		trimmed = errsStripDotnetProject(trimmed)

		if parsed, ok := errsParseDotnetLocated(trimmed); ok {
			key := fmt.Sprintf("%s:%d:%s", parsed.File, parsed.Line, parsed.Message)
			if !seen[key] {
				seen[key] = true
				errors = append(errors, parsed)
			}
			continue
		}
		if parsed, ok := errsParseDotnetUnlocated(trimmed); ok {
			key := fmt.Sprintf("%s:0:%s", parsed.File, parsed.Message)
			if !seen[key] {
				seen[key] = true
				errors = append(errors, parsed)
			}
		}
	}

	return errsBuildResult(errors, "dotnet")
}

func errsParseDotnetLocated(line string) (errsParsedError, bool) {
	parenIdx := strings.Index(line, "(")
	if parenIdx < 1 {
		return errsParsedError{}, false
	}
	closeIdx := strings.Index(line[parenIdx:], ")")
	if closeIdx < 1 {
		return errsParsedError{}, false
	}

	file := line[:parenIdx]
	inner := line[parenIdx+1 : parenIdx+closeIdx]
	parts := strings.SplitN(inner, ",", 2)
	if len(parts) != 2 {
		return errsParsedError{}, false
	}

	lineNum, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	col, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return errsParsedError{}, false
	}

	rest := line[parenIdx+closeIdx+1:]
	if !strings.HasPrefix(rest, ": ") {
		return errsParsedError{}, false
	}
	rest = rest[2:]

	severity, code, message := errsParseDotnetMsg(rest)
	if message == "" {
		return errsParsedError{}, false
	}

	return errsParsedError{
		File: file, Line: lineNum, Col: col,
		Code: code, Severity: severity, Message: message,
	}, true
}

func errsParseDotnetUnlocated(line string) (errsParsedError, bool) {
	rest := line
	source := ""
	if sepIdx := strings.Index(line, " : "); sepIdx > 0 {
		source = strings.TrimSpace(line[:sepIdx])
		rest = line[sepIdx+3:]
	}

	severity, code, message := errsParseDotnetMsg(rest)
	if message == "" {
		return errsParsedError{}, false
	}

	file := source
	if file == "" {
		file = "(build)"
	}

	return errsParsedError{
		File: file, Code: code, Severity: severity, Message: message,
	}, true
}

func errsParseDotnetMsg(s string) (string, string, string) {
	severity := ""
	rest := s
	if strings.HasPrefix(rest, "error ") {
		severity = "error"
		rest = rest[6:]
	} else if strings.HasPrefix(rest, "warning ") {
		severity = "warning"
		rest = rest[8:]
	} else {
		return "", "", ""
	}

	colonIdx := strings.Index(rest, ": ")
	if colonIdx < 1 {
		return severity, "", strings.TrimSpace(rest)
	}
	return severity, rest[:colonIdx], strings.TrimSpace(rest[colonIdx+2:])
}

// --- ESLint parser ---

func errsParseESLint(lines []string) *errsResult {
	var errors []errsParsedError
	seen := make(map[string]bool)
	currentFile := ""

	for _, line := range lines {
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "\u2716") {
			continue
		}

		if line == trimmed {
			if !strings.Contains(trimmed, " ") || strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
				fields := strings.Fields(trimmed)
				if len(fields) > 0 {
					locParts := strings.SplitN(fields[0], ":", 2)
					isDetail := false
					if len(locParts) == 2 {
						_, e1 := strconv.Atoi(locParts[0])
						_, e2 := strconv.Atoi(locParts[1])
						isDetail = e1 == nil && e2 == nil
					}
					if !isDetail {
						currentFile = trimmed
						continue
					}
				}
			}
		}

		if currentFile != "" && line != trimmed {
			fields := strings.Fields(trimmed)
			if len(fields) < 4 {
				continue
			}

			locParts := strings.SplitN(fields[0], ":", 2)
			if len(locParts) != 2 {
				continue
			}

			lineNum, err1 := strconv.Atoi(locParts[0])
			col, err2 := strconv.Atoi(locParts[1])
			if err1 != nil || err2 != nil {
				continue
			}

			severity := fields[1]
			if severity != "error" && severity != "warning" {
				continue
			}

			rule := fields[len(fields)-1]
			message := strings.Join(fields[2:len(fields)-1], " ")

			key := fmt.Sprintf("%s:%d:%s", currentFile, lineNum, message)
			if seen[key] {
				continue
			}
			seen[key] = true

			errors = append(errors, errsParsedError{
				File: currentFile, Line: lineNum, Col: col,
				Code: rule, Severity: severity, Message: message,
			})
		}
	}

	return errsBuildResult(errors, "eslint")
}

// --- Result builder ---

func errsBuildResult(errors []errsParsedError, format string) *errsResult {
	if errors == nil {
		errors = []errsParsedError{}
	}

	fileSet := make(map[string]bool)
	errCount := 0
	warnCount := 0
	for _, e := range errors {
		fileSet[e.File] = true
		if e.Severity == "warning" || e.Severity == "note" {
			warnCount++
		} else {
			errCount++
		}
	}

	files := len(fileSet)
	count := len(errors)

	var summary string
	if count == 0 {
		summary = "no errors found"
	} else {
		var parts []string
		if errCount > 0 {
			w := "errors"
			if errCount == 1 {
				w = "error"
			}
			parts = append(parts, fmt.Sprintf("%d %s", errCount, w))
		}
		if warnCount > 0 {
			w := "warnings"
			if warnCount == 1 {
				w = "warning"
			}
			parts = append(parts, fmt.Sprintf("%d %s", warnCount, w))
		}
		fw := "files"
		if files == 1 {
			fw = "file"
		}
		summary = fmt.Sprintf("%s in %d %s", strings.Join(parts, ", "), files, fw)
	}

	return &errsResult{
		Errors: errors, Format: format, Count: count, Files: files, Summary: summary,
	}
}

// --- Helpers ---

func errsStripANSI(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

func errsParseLocation(s string) (string, int, int) {
	startSearch := 0
	if len(s) > 2 && errsIsLetter(s[0]) && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		startSearch = 2
	}

	colonIdx := -1
	for i := startSearch; i < len(s); i++ {
		if s[i] == ':' && i+1 < len(s) && errsIsDigit(s[i+1]) {
			colonIdx = i
			break
		}
	}
	if colonIdx < 1 {
		return "", 0, 0
	}

	file := s[:colonIdx]
	rest := s[colonIdx+1:]

	lineNum, consumed := errsParseNumber(rest)
	rest = rest[consumed:]

	col := 0
	if len(rest) > 1 && rest[0] == ':' && errsIsDigit(rest[1]) {
		rest = rest[1:]
		col, _ = errsParseNumber(rest)
	}

	return file, lineNum, col
}

func errsParseNumber(s string) (int, int) {
	n := 0
	i := 0
	for i < len(s) && errsIsDigit(s[i]) {
		n = n*10 + int(s[i]-'0')
		i++
	}
	return n, i
}

func errsIsDigit(b byte) bool  { return b >= '0' && b <= '9' }
func errsIsLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func errsLooksLikeCode(s string) bool {
	if len(s) < 2 || len(s) > 15 {
		return false
	}
	hasDigit := false
	hasLetter := false
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			hasLetter = true
		case c == '-' || c == '_':
			// ok
		default:
			return false
		}
	}
	return hasDigit && hasLetter
}

func errsHasColonDigit(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == ':' && errsIsDigit(s[i+1]) {
			return true
		}
	}
	return false
}

func errsStripDotnetProject(s string) string {
	if !strings.HasSuffix(s, "]") {
		return s
	}
	bracketStart := strings.LastIndex(s, " [")
	if bracketStart < 0 {
		return s
	}
	return s[:bracketStart]
}

func errsHasDotnetProject(s string) bool {
	if !strings.HasSuffix(s, "]") {
		return false
	}
	bracketStart := strings.LastIndex(s, "[")
	if bracketStart < 0 {
		return false
	}
	project := s[bracketStart+1 : len(s)-1]
	return strings.HasSuffix(project, ".csproj") ||
		strings.HasSuffix(project, ".fsproj") ||
		strings.HasSuffix(project, ".vbproj")
}

func errsIsDotnetCode(after string) bool {
	prefixes := []string{
		": error CS", ": warning CS", ": error IDE", ": warning IDE",
		": error CA", ": warning CA", ": error SA", ": warning SA",
		": error MSB", ": warning MSB", ": error BC", ": warning BC",
		": error NETSDK", ": warning NETSDK", ": error NU", ": warning NU",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(after, p) {
			return true
		}
	}
	return false
}
