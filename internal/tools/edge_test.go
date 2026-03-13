package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Read edge cases
// =============================================================================

func TestRead_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, []byte{}, 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("empty file should be readable: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)

	// Empty file split by \n yields [""], so total_lines=1
	if int(out["total_lines"].(float64)) != 1 {
		t.Errorf("expected 1 total_lines for empty file, got %v", out["total_lines"])
	}
}

func TestRead_SingleLineNoNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.txt")
	os.WriteFile(path, []byte("no trailing newline"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if int(out["total_lines"].(float64)) != 1 {
		t.Errorf("expected 1 total_lines, got %v", out["total_lines"])
	}
	content := out["content"].(string)
	if !strings.Contains(content, "no trailing newline") {
		t.Error("content should contain the line")
	}
}

func TestRead_WindowsLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	os.WriteFile(path, []byte("line1\r\nline2\r\nline3\r\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	// \r\n split by \n yields "line1\r", "line2\r", "line3\r", ""
	// Content should still contain the text, even with \r chars
	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	content := out["content"].(string)
	if !strings.Contains(content, "line1") {
		t.Error("should contain line1 even with CRLF")
	}
	if !strings.Contains(content, "line2") {
		t.Error("should contain line2 even with CRLF")
	}
}

func TestRead_BinaryContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.dat")
	data := make([]byte, 256)
	for i := range 256 {
		data[i] = byte(i)
	}
	os.WriteFile(path, data, 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	// Should not error — read is content-agnostic
	if result.IsError {
		t.Fatalf("binary file should be readable: %s", result.Error)
	}
	// Output should be valid JSON despite binary content
	if !json.Valid([]byte(result.Output)) {
		t.Error("output should be valid JSON even for binary content")
	}
}

func TestRead_VeryLongLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "longline.txt")
	longLine := strings.Repeat("x", 5000)
	os.WriteFile(path, []byte(longLine), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	content := out["content"].(string)

	// Line should be truncated at 2000 chars
	if !strings.Contains(content, "... (truncated)") {
		t.Error("expected line truncation marker for 5000-char line")
	}
}

func TestRead_NegativeOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"offset":    float64(-5),
	}, "")

	// Negative offset should clamp to 1, not crash
	if result.IsError {
		t.Fatalf("negative offset should not error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if int(out["from_line"].(float64)) != 1 {
		t.Errorf("negative offset should clamp to 1, got from_line=%v", out["from_line"])
	}
}

func TestRead_ZeroOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"offset":    float64(0),
	}, "")

	if result.IsError {
		t.Fatalf("zero offset should not error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if int(out["from_line"].(float64)) != 1 {
		t.Errorf("zero offset should clamp to 1, got from_line=%v", out["from_line"])
	}
}

func TestRead_ZeroLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"limit":     float64(0),
	}, "")

	// limit=0 should use default (2000), not read 0 lines
	if result.IsError {
		t.Fatalf("zero limit should not error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	content := out["content"].(string)
	if !strings.Contains(content, "line1") {
		t.Error("zero limit should default to 2000, not return empty")
	}
}

func TestRead_NegativeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"limit":     float64(-10),
	}, "")

	if result.IsError {
		t.Fatalf("negative limit should not error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	content := out["content"].(string)
	if !strings.Contains(content, "line1") {
		t.Error("negative limit should default to 2000, not return empty")
	}
}

func TestRead_OffsetEqualsLastLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	// 3 lines, no trailing newline = total_lines=3
	os.WriteFile(path, []byte("line1\nline2\nline3"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"offset":    float64(3),
		"limit":     float64(1),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	content := out["content"].(string)
	if !strings.Contains(content, "line3") {
		t.Error("offset=last line should return last line")
	}
}

func TestRead_OffsetExactlyOneAfterEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	// "a\nb\n" splits to ["a", "b", ""], total=3
	os.WriteFile(path, []byte("a\nb\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"offset":    float64(4), // one past total_lines=3
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if out["note"] == nil {
		t.Error("should have a 'beyond end of file' note")
	}
}

func TestRead_FilePathIsDirectory(t *testing.T) {
	dir := t.TempDir()

	result := builtinRead(context.Background(), map[string]any{
		"file_path": dir,
	}, "")

	if !result.IsError {
		t.Fatal("reading a directory should error")
	}
}

func TestRead_FilePathWithSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file with spaces.txt")
	os.WriteFile(path, []byte("content here"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("file with spaces should be readable: %s", result.Error)
	}
}

func TestRead_UnicodeContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unicode.txt")
	os.WriteFile(path, []byte("Hello\nWorld\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("unicode content should be readable: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	content := out["content"].(string)
	if !strings.Contains(content, "Hello") {
		t.Error("should contain unicode emoji")
	}
}

func TestRead_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noperm.txt")
	os.WriteFile(path, []byte("secret"), 0000)
	defer os.Chmod(path, 0644) // cleanup

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if !result.IsError {
		t.Fatal("expected permission denied error")
	}
}

func TestRead_FilePathWrongType(t *testing.T) {
	// Claude might send a number instead of a string
	result := builtinRead(context.Background(), map[string]any{
		"file_path": float64(42),
	}, "")

	if !result.IsError {
		t.Fatal("non-string file_path should error")
	}
}

func TestRead_OffsetAsStringNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	// LLMs sometimes send "3" instead of 3
	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"offset":    "3",
	}, "")

	// toInt handles string -> int conversion
	if result.IsError {
		t.Fatalf("string offset should work via toInt: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if int(out["from_line"].(float64)) != 3 {
		t.Errorf("string offset '3' should resolve to from_line=3, got %v", out["from_line"])
	}
}

func TestRead_OnlyNewlines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "newlines.txt")
	os.WriteFile(path, []byte("\n\n\n\n\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("file of only newlines should be readable: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	// 5 newlines split by \n = 6 elements (including trailing empty)
	if int(out["total_lines"].(float64)) != 6 {
		t.Errorf("expected 6 total_lines, got %v", out["total_lines"])
	}
}

func TestRead_NilInputMap(t *testing.T) {
	result := builtinRead(context.Background(), nil, "")
	if !result.IsError {
		t.Fatal("nil input should error")
	}
}

func TestRead_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before call

	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	// Read is synchronous file I/O; cancelled context shouldn't crash,
	// though it may or may not propagate the cancellation
	result := builtinRead(ctx, map[string]any{
		"file_path": path,
	}, "")

	// Either success or clean error — must not panic
	_ = result
}

// =============================================================================
// Write edge cases
// =============================================================================

func TestWrite_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   "",
	}, "")

	if result.IsError {
		t.Fatalf("empty content should be valid: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if int(out["bytes_written"].(float64)) != 0 {
		t.Errorf("expected 0 bytes written, got %v", out["bytes_written"])
	}

	data, _ := os.ReadFile(path)
	if len(data) != 0 {
		t.Error("file should be empty")
	}
}

func TestWrite_ContentWithNullBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nulls.bin")

	content := "before\x00after"
	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   content,
	}, "")

	if result.IsError {
		t.Fatalf("null byte content should work: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != content {
		t.Error("null bytes should be preserved")
	}
}

func TestWrite_UnicodeContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unicode.txt")

	content := "CJK: CJK test\nThai: Thai test\nArabic: Arabic test"
	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   content,
	}, "")

	if result.IsError {
		t.Fatalf("unicode content should work: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != content {
		t.Errorf("unicode content should be preserved, got: %q", string(data))
	}
}

func TestWrite_ContentWrongType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Claude sends a number instead of string
	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   float64(42),
	}, "")

	if !result.IsError {
		t.Fatal("non-string content should error")
	}
}

func TestWrite_FilePathWrongType(t *testing.T) {
	result := builtinWrite(context.Background(), map[string]any{
		"file_path": []string{"/tmp/test.txt"},
		"content":   "hello",
	}, "")

	if !result.IsError {
		t.Fatal("non-string file_path should error")
	}
}

func TestWrite_ReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	os.MkdirAll(roDir, 0555)
	defer os.Chmod(roDir, 0755) // cleanup
	path := filepath.Join(roDir, "test.txt")

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   "should fail",
	}, "")

	if !result.IsError {
		t.Fatal("writing to read-only directory should error")
	}
}

func TestWrite_PathIsExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	os.MkdirAll(subDir, 0755)

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": subDir,
		"content":   "should fail",
	}, "")

	if !result.IsError {
		t.Fatal("writing to a path that is a directory should error")
	}
}

func TestWrite_LargeContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	content := strings.Repeat("x", 100_000)

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   content,
	}, "")

	if result.IsError {
		t.Fatalf("large content should work: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if int(out["bytes_written"].(float64)) != 100_000 {
		t.Errorf("expected 100000 bytes written, got %v", out["bytes_written"])
	}

	data, _ := os.ReadFile(path)
	if len(data) != 100_000 {
		t.Errorf("file should be 100000 bytes, got %d", len(data))
	}
}

func TestWrite_NilInputMap(t *testing.T) {
	result := builtinWrite(context.Background(), nil, "")
	if !result.IsError {
		t.Fatal("nil input should error")
	}
}

// =============================================================================
// Bash edge cases
// =============================================================================

func TestBash_EmptyString(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "",
	}, "")

	if !result.IsError {
		t.Fatal("empty command should error")
	}
}

func TestBash_WhitespaceOnlyCommand(t *testing.T) {
	// Whitespace-only is technically a valid shell command (no-op)
	result := builtinBash(context.Background(), map[string]any{
		"command": "   ",
	}, "")

	// Shell executes "   " which is a no-op, exit 0
	if result.IsError {
		t.Logf("whitespace command error (acceptable): %s", result.Error)
	}
}

func TestBash_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name    string
		command string
		expect  string
	}{
		{"single_quotes", `echo 'hello world'`, "hello world"},
		{"double_quotes", `echo "hello world"`, "hello world"},
		{"backticks", "echo `echo nested`", "nested"},
		{"dollar_expansion", `echo $((2+3))`, "5"},
		{"semicolons", `echo first; echo second`, "first\nsecond"},
		{"ampersand", `echo bg && echo fg`, "bg\nfg"},
		// Note: zsh errors on unmatched globs (unlike bash which echoes them literal).
		// This is tested separately in TestBash_ZshGlobNoMatch.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builtinBash(context.Background(), map[string]any{
				"command": tt.command,
			}, "")

			if result.IsError {
				t.Fatalf("command %q failed: %s", tt.command, result.Error)
			}

			var out map[string]any
			json.Unmarshal([]byte(result.Output), &out)
			stdout := strings.TrimSpace(out["stdout"].(string))
			if stdout != tt.expect {
				t.Errorf("expected %q, got %q", tt.expect, stdout)
			}
		})
	}
}

func TestBash_NonZeroWithOnlyStdout(t *testing.T) {
	// grep returns exit 1 when no match, writes nothing to stderr
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo 'hello' | grep 'xyz'",
	}, "")

	if !result.IsError {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(result.Error, "exit code 1") {
		t.Errorf("expected exit code 1, got: %s", result.Error)
	}
}

func TestBash_NonZeroWithBothStreams(t *testing.T) {
	// Writes to both stdout and stderr, then exits non-zero
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo out_msg; echo err_msg >&2; exit 7",
	}, "")

	if !result.IsError {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(result.Error, "exit code 7") {
		t.Errorf("expected exit code 7, got: %s", result.Error)
	}
	// Both streams should be in the error output
	if !strings.Contains(result.Error, "err_msg") {
		t.Error("expected stderr content in error")
	}
	if !strings.Contains(result.Error, "out_msg") {
		t.Error("expected stdout content in error for dual-stream non-zero exit")
	}
}

func TestBash_MultilineCommand(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "for i in 1 2 3; do\necho $i\ndone",
	}, "")

	if result.IsError {
		t.Fatalf("multiline command should work: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	stdout := strings.TrimSpace(out["stdout"].(string))
	if stdout != "1\n2\n3" {
		t.Errorf("expected '1\\n2\\n3', got %q", stdout)
	}
}

func TestBash_EnvironmentVariables(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo $HOME",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	stdout := strings.TrimSpace(out["stdout"].(string))
	if stdout == "" || stdout == "$HOME" {
		t.Error("$HOME should be expanded by the shell")
	}
}

func TestBash_CustomTimeoutOverride(t *testing.T) {
	start := time.Now()
	result := builtinBash(context.Background(), map[string]any{
		"command": "sleep 30",
		"timeout": float64(1), // 1 second override
	}, "")
	elapsed := time.Since(start)

	if !result.IsError {
		t.Fatal("expected timeout error")
	}
	// Should timeout in ~1s, not 30s or 120s (default)
	if elapsed > 5*time.Second {
		t.Errorf("timeout override didn't work — elapsed %s", elapsed)
	}
}

func TestBash_CommandWrongType(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": 42,
	}, "")

	if !result.IsError {
		t.Fatal("non-string command should error")
	}
}

func TestBash_NilInputMap(t *testing.T) {
	result := builtinBash(context.Background(), nil, "")
	if !result.IsError {
		t.Fatal("nil input should error")
	}
}

func TestBash_LargeStdout(t *testing.T) {
	// Generate >50KB of output to test truncation path in executeBuiltin
	result := builtinBash(context.Background(), map[string]any{
		// yes outputs "y\n" repeatedly; head -c limits output
		"command": "head -c 60000 /dev/zero | tr '\\0' 'A'",
	}, "")

	if result.IsError {
		t.Fatalf("large output should succeed: %s", result.Error)
	}

	// The raw JSON output contains the 60000 chars embedded in a JSON string.
	// After JSON encoding overhead, total output exceeds MaxOutputBytes.
	// The executeBuiltin wrapper should truncate.
	// But the builtin itself returns a JSON-encoded result, so the actual
	// stdout field inside the JSON is 60000 bytes before encoding.
	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	stdout := out["stdout"].(string)
	if len(stdout) < 50000 {
		t.Errorf("expected at least 50000 chars stdout, got %d", len(stdout))
	}
}

func TestBash_ExitCode127_CommandNotFound(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "nonexistent_command_xyz_123",
	}, "")

	if !result.IsError {
		t.Fatal("unknown command should error")
	}
	if !strings.Contains(result.Error, "127") {
		t.Errorf("expected exit code 127, got: %s", result.Error)
	}
}

func TestBash_ExitCode2_ExplicitExit(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "exit 2",
	}, "")

	if !result.IsError {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(result.Error, "exit code 2") {
		t.Errorf("expected exit code 2, got: %s", result.Error)
	}
}

// zsh treats unmatched globs as fatal errors, unlike bash.
// This is the correct behavior and an important edge case.
func TestBash_ZshGlobNoMatch(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo /nonexistent_glob_pattern_xyz_*",
	}, "")

	// zsh: "no matches found" — non-zero exit
	if !result.IsError {
		t.Log("Shell is bash (glob echoed literally); zsh would error here")
		return
	}
	if !strings.Contains(result.Error, "no matches found") {
		t.Logf("got error (expected): %s", result.Error)
	}
}

func TestBash_NonExistentWorkDir(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo hello",
	}, "/nonexistent/workdir/path")

	if !result.IsError {
		t.Fatal("non-existent workdir should error")
	}
}

func TestBash_AlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := builtinBash(ctx, map[string]any{
		"command": "echo hello",
	}, "")

	// Should error cleanly, not panic
	if !result.IsError {
		t.Fatal("cancelled context should produce error")
	}
}

// =============================================================================
// buildArgs edge cases
// =============================================================================

func TestBuildArgs_NilInput(t *testing.T) {
	def := ToolDef{
		NeedsCLI: true,
		FlagMap:  map[string]FlagSpec{},
	}

	args := buildArgs(def, nil)

	// Should get just --cli, no panic
	if len(args) != 1 || args[0] != "--cli" {
		t.Errorf("nil input should produce [--cli], got %v", args)
	}
}

func TestBuildArgs_EmptyInput(t *testing.T) {
	def := ToolDef{
		NeedsCLI: false,
		FlagMap: map[string]FlagSpec{
			"file": {Flag: "--file", Type: "string"},
		},
	}

	args := buildArgs(def, map[string]any{})

	if len(args) != 0 {
		t.Errorf("empty input should produce no args, got %v", args)
	}
}

func TestBuildArgs_BoolAsStringTrue(t *testing.T) {
	def := ToolDef{
		FlagMap: map[string]FlagSpec{
			"verbose": {Flag: "--verbose", Type: "bool"},
		},
	}

	// Claude might send "true" (string) instead of true (bool)
	args := buildArgs(def, map[string]any{
		"verbose": "true",
	})

	// String "true" is not a bool — should be ignored
	assertNotContains(t, args, "--verbose")
}

func TestBuildArgs_IntAsString(t *testing.T) {
	def := ToolDef{
		FlagMap: map[string]FlagSpec{
			"depth": {Flag: "--depth", Type: "int"},
		},
	}

	// toInt handles string -> int
	args := buildArgs(def, map[string]any{
		"depth": "5",
	})

	assertArgsContain(t, args, []string{"--depth", "5"})
}

func TestBuildArgs_NegativeInt(t *testing.T) {
	def := ToolDef{
		FlagMap: map[string]FlagSpec{
			"depth": {Flag: "--depth", Type: "int"},
		},
	}

	args := buildArgs(def, map[string]any{
		"depth": float64(-1),
	})

	// Negative ints are non-zero, so they should pass through
	assertArgsContain(t, args, []string{"--depth", "-1"})
}

func TestBuildArgs_EmptyStringSkipped(t *testing.T) {
	def := ToolDef{
		FlagMap: map[string]FlagSpec{
			"file": {Flag: "--file", Type: "string"},
		},
	}

	args := buildArgs(def, map[string]any{
		"file": "",
	})

	// Empty strings should be skipped
	assertNotContains(t, args, "--file")
}

func TestBuildArgs_EmptyArraySkipped(t *testing.T) {
	def := ToolDef{
		FlagMap: map[string]FlagSpec{
			"dirs": {Flag: "--dir", Type: "array"},
		},
	}

	args := buildArgs(def, map[string]any{
		"dirs": []any{},
	})

	assertNotContains(t, args, "--dir")
}

func TestBuildArgs_SingleElementArray(t *testing.T) {
	def := ToolDef{
		FlagMap: map[string]FlagSpec{
			"file": {Flag: "--file", Type: "array"},
		},
	}

	args := buildArgs(def, map[string]any{
		"file": []any{"/tmp/test.go"},
	})

	// Single element: no trailing comma
	assertArgsContain(t, args, []string{"--file", "/tmp/test.go"})
}

func TestBuildArgs_AllTypesAtOnce(t *testing.T) {
	def := ToolDef{
		NeedsCLI:   true,
		StdinParam: "stdin_val",
		FlagMap: map[string]FlagSpec{
			"str":       {Flag: "--str", Type: "string"},
			"b":         {Flag: "--bool", Type: "bool"},
			"num":       {Flag: "--num", Type: "int"},
			"arr":       {Flag: "--arr", Type: "array"},
			"pos":       {Type: "string", Positional: true},
			"stdin_val": {Flag: "--stdin", Type: "string"},
		},
	}

	args := buildArgs(def, map[string]any{
		"str":       "hello",
		"b":         true,
		"num":       float64(42),
		"arr":       []any{"a", "b"},
		"pos":       "positional_value",
		"stdin_val": "should be skipped",
	})

	assertContains(t, args, "--cli")
	assertContains(t, args, "--bool")
	assertNotContains(t, args, "--stdin")
	assertNotContains(t, args, "should be skipped")

	// Positional arg at end
	last := args[len(args)-1]
	if last != "positional_value" {
		t.Errorf("positional arg should be last, got %q", last)
	}
}

func TestBuildArgs_NilFlagMap(t *testing.T) {
	def := ToolDef{
		NeedsCLI: false,
		FlagMap:  nil,
	}

	args := buildArgs(def, map[string]any{
		"anything": "value",
	})

	// Nil FlagMap means nothing matches — should be empty
	if len(args) != 0 {
		t.Errorf("nil FlagMap should produce no args, got %v", args)
	}
}

// =============================================================================
// Executor edge cases
// =============================================================================

func TestExecutor_BuiltinTimeoutPropagation(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{
		Name:    "slow_builtin",
		Timeout: 200 * time.Millisecond,
		Builtin: func(ctx context.Context, input map[string]any, workDir string) Result {
			select {
			case <-ctx.Done():
				return Result{IsError: true, Error: "cancelled"}
			case <-time.After(5 * time.Second):
				return Result{Output: "should not reach"}
			}
		},
	})
	exec := NewExecutor(reg, "")

	start := time.Now()
	result := exec.Execute(context.Background(), "slow_builtin", nil)
	elapsed := time.Since(start)

	if !result.IsError {
		t.Fatal("expected timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("executeBuiltin timeout didn't work — elapsed %s", elapsed)
	}
}

func TestExecutor_BuiltinOutputTruncation(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{
		Name: "big_output",
		Builtin: func(ctx context.Context, input map[string]any, workDir string) Result {
			// Return >50KB of output
			return Result{Output: strings.Repeat("x", MaxOutputBytes+1000)}
		},
	})
	exec := NewExecutor(reg, "")

	result := exec.Execute(context.Background(), "big_output", nil)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if len(result.Output) > MaxOutputBytes+100 { // some room for truncation message
		t.Errorf("output should be truncated to ~%d bytes, got %d", MaxOutputBytes, len(result.Output))
	}
	if !strings.Contains(result.Output, "truncated") {
		t.Error("truncated output should contain truncation marker")
	}
}

func TestExecutor_BuiltinErrorPassthrough(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{
		Name: "error_builtin",
		Builtin: func(ctx context.Context, input map[string]any, workDir string) Result {
			return Result{IsError: true, Error: "custom error message"}
		},
	})
	exec := NewExecutor(reg, "")

	result := exec.Execute(context.Background(), "error_builtin", nil)

	if !result.IsError {
		t.Fatal("expected error")
	}
	if result.Error != "custom error message" {
		t.Errorf("expected custom error, got: %s", result.Error)
	}
}

func TestExecutor_ConcurrentBuiltinAccess(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	exec := NewExecutor(reg, dir)

	// Write a file, then read it concurrently from multiple goroutines
	path := filepath.Join(dir, "concurrent.txt")
	os.WriteFile(path, []byte("concurrent content\n"), 0644)

	var wg sync.WaitGroup
	errs := make(chan string, 20)

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := exec.Execute(context.Background(), "read", map[string]any{
				"file_path": path,
			})
			if result.IsError {
				errs <- result.Error
			}
		}()
	}

	wg.Wait()
	close(errs)

	for errMsg := range errs {
		t.Errorf("concurrent read error: %s", errMsg)
	}
}

func TestExecutor_BuiltinWithNilInput(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ReadDef)
	exec := NewExecutor(reg, "")

	// nil input should produce a clean error, not a panic
	result := exec.Execute(context.Background(), "read", nil)
	if !result.IsError {
		t.Fatal("nil input to read should error")
	}
}

// =============================================================================
// Registry edge cases
// =============================================================================

func TestRegistry_RegisterReplace(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{Name: "tool", Description: "first"})
	reg.Register(ToolDef{Name: "tool", Description: "second"})

	if reg.Len() != 1 {
		t.Errorf("replace should not increase count, got %d", reg.Len())
	}

	def, _ := reg.Get("tool")
	if def.Description != "second" {
		t.Errorf("expected replaced description, got %q", def.Description)
	}

	// Order should still only have one entry
	apiTools := reg.APITools()
	if len(apiTools) != 1 {
		t.Errorf("APITools should have 1, got %d", len(apiTools))
	}
}

func TestRegistry_RemoveNonexistent(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{Name: "a"})

	// Should not panic
	reg.Remove("nonexistent")

	if reg.Len() != 1 {
		t.Errorf("removing nonexistent should not affect count, got %d", reg.Len())
	}
}

func TestRegistry_GetFromEmpty(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.Get("anything")
	if ok {
		t.Error("empty registry should return false for any Get")
	}
}

func TestRegistry_NamesFromEmpty(t *testing.T) {
	reg := NewRegistry()
	names := reg.Names()
	if len(names) != 0 {
		t.Errorf("empty registry should have no names, got %v", names)
	}
}

func TestRegistry_APIToolsFromEmpty(t *testing.T) {
	reg := NewRegistry()
	tools := reg.APITools()
	if len(tools) != 0 {
		t.Errorf("empty registry should have no API tools, got %d", len(tools))
	}
}

func TestRegistry_RegisterRemoveRegister(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{Name: "x", Description: "v1"})
	reg.Remove("x")
	reg.Register(ToolDef{Name: "x", Description: "v2"})

	if reg.Len() != 1 {
		t.Errorf("expected 1 tool after re-register, got %d", reg.Len())
	}

	def, ok := reg.Get("x")
	if !ok {
		t.Fatal("re-registered tool should be found")
	}
	if def.Description != "v2" {
		t.Errorf("expected v2 description, got %q", def.Description)
	}
}

func TestRegistry_RemovePreservesOtherOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{Name: "a"})
	reg.Register(ToolDef{Name: "b"})
	reg.Register(ToolDef{Name: "c"})

	reg.Remove("b")

	tools := reg.APITools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "a" || tools[1].Name != "c" {
		t.Errorf("expected order [a, c], got [%s, %s]", tools[0].Name, tools[1].Name)
	}
}

// =============================================================================
// toInt edge cases
// =============================================================================

func TestToInt_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want int
	}{
		{"float64", float64(42), 42},
		{"float64_negative", float64(-7), -7},
		{"float64_fractional", float64(3.9), 3}, // truncates, doesn't round
		{"int", 99, 99},
		{"string_number", "123", 123},
		{"string_negative", "-5", -5},
		{"string_invalid", "abc", 0},
		{"string_empty", "", 0},
		{"nil", nil, 0},
		{"bool", true, 0},
		{"slice", []any{1}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toInt(tt.val)
			if got != tt.want {
				t.Errorf("toInt(%v) = %d, want %d", tt.val, got, tt.want)
			}
		})
	}
}

// =============================================================================
// resolvePath edge cases
// =============================================================================

func TestResolvePath_Absolute(t *testing.T) {
	got := resolvePath("/absolute/path", "/workdir")
	if got != "/absolute/path" {
		t.Errorf("absolute path should be unchanged, got %q", got)
	}
}

func TestResolvePath_RelativeWithWorkDir(t *testing.T) {
	got := resolvePath("relative/file.txt", "/workdir")
	if got != "/workdir/relative/file.txt" {
		t.Errorf("expected joined path, got %q", got)
	}
}

func TestResolvePath_RelativeNoWorkDir(t *testing.T) {
	got := resolvePath("relative/file.txt", "")
	if got != "relative/file.txt" {
		t.Errorf("no workdir should return as-is, got %q", got)
	}
}

func TestResolvePath_DotRelative(t *testing.T) {
	got := resolvePath("./file.txt", "/workdir")
	if got != "/workdir/file.txt" {
		t.Errorf("expected resolved dot path, got %q", got)
	}
}

func TestResolvePath_EmptyPath(t *testing.T) {
	got := resolvePath("", "/workdir")
	// filepath.Join("/workdir", "") = "/workdir"
	if got != "/workdir" {
		t.Errorf("empty path with workdir should resolve to workdir, got %q", got)
	}
}

// =============================================================================
// Integration: write + bash verify + read roundtrip
// =============================================================================

func TestIntegration_WriteBashReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.txt")

	// 1. Write a file
	writeResult := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   "hello from write\n",
	}, "")
	if writeResult.IsError {
		t.Fatalf("write failed: %s", writeResult.Error)
	}

	// 2. Use bash to verify and append
	bashResult := builtinBash(context.Background(), map[string]any{
		"command": "cat " + path + " && echo 'appended by bash' >> " + path,
	}, "")
	if bashResult.IsError {
		t.Fatalf("bash failed: %s", bashResult.Error)
	}
	var bashOut map[string]any
	json.Unmarshal([]byte(bashResult.Output), &bashOut)
	if !strings.Contains(bashOut["stdout"].(string), "hello from write") {
		t.Error("bash cat should show written content")
	}

	// 3. Read back the combined file
	readResult := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")
	if readResult.IsError {
		t.Fatalf("read failed: %s", readResult.Error)
	}
	var readOut map[string]any
	json.Unmarshal([]byte(readResult.Output), &readOut)
	content := readOut["content"].(string)
	if !strings.Contains(content, "hello from write") {
		t.Error("should contain original write content")
	}
	if !strings.Contains(content, "appended by bash") {
		t.Error("should contain bash-appended content")
	}
}
