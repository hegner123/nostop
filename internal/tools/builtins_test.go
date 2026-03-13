package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuiltinRead_BasicFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line one\nline two\nline three\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("invalid JSON output: %s", err)
	}

	if int(out["total_lines"].(float64)) != 4 { // 3 lines + trailing newline = 4
		t.Errorf("expected 4 total_lines, got %v", out["total_lines"])
	}

	content := out["content"].(string)
	if !strings.Contains(content, "line one") {
		t.Error("expected content to contain 'line one'")
	}
	if !strings.Contains(content, "line three") {
		t.Error("expected content to contain 'line three'")
	}
}

func TestBuiltinRead_WithOffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line "+toString(i))
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"offset":    float64(3),
		"limit":     float64(2),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)

	if int(out["from_line"].(float64)) != 3 {
		t.Errorf("expected from_line 3, got %v", out["from_line"])
	}
	// offset=3 (startIdx=2), limit=2 -> endIdx=4, so to_line=4
	if int(out["to_line"].(float64)) != 4 {
		t.Errorf("expected to_line 4, got %v", out["to_line"])
	}

	content := out["content"].(string)
	if !strings.Contains(content, "line 3") {
		t.Error("expected content to contain 'line 3'")
	}
	if !strings.Contains(content, "line 4") {
		t.Error("expected content to contain 'line 4'")
	}
	// line 5 is at index 4, which equals endIdx — it should NOT be included
	if strings.Contains(content, "line 5") {
		t.Error("expected content to NOT contain 'line 5'")
	}
}

func TestBuiltinRead_NonexistentFile(t *testing.T) {
	result := builtinRead(context.Background(), map[string]any{
		"file_path": "/nonexistent/path/to/file.txt",
	}, "")

	if !result.IsError {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(result.Error, "failed to read file") {
		t.Errorf("unexpected error message: %s", result.Error)
	}
}

func TestBuiltinRead_RelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.txt")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("hello"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": "sub/test.txt",
	}, dir)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	content := out["content"].(string)
	if !strings.Contains(content, "hello") {
		t.Error("expected content to contain 'hello'")
	}
}

func TestBuiltinRead_MissingFilePath(t *testing.T) {
	result := builtinRead(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing file_path")
	}
}

func TestBuiltinRead_OffsetBeyondEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	os.WriteFile(path, []byte("one\ntwo\n"), 0644)

	result := builtinRead(context.Background(), map[string]any{
		"file_path": path,
		"offset":    float64(100),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if out["note"] == nil {
		t.Error("expected a note about offset beyond EOF")
	}
}

func TestBuiltinWrite_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   "hello world\n",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if out["status"] != "ok" {
		t.Errorf("expected status ok, got %v", out["status"])
	}
	if int(out["bytes_written"].(float64)) != 12 {
		t.Errorf("expected 12 bytes written, got %v", out["bytes_written"])
	}

	// Verify file was actually written
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %s", err)
	}
	if string(data) != "hello world\n" {
		t.Errorf("file content mismatch: %q", string(data))
	}
}

func TestBuiltinWrite_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "deep.txt")

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   "deep content",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %s", err)
	}
	if string(data) != "deep content" {
		t.Errorf("content mismatch: %q", string(data))
	}
}

func TestBuiltinWrite_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	os.WriteFile(path, []byte("old content"), 0644)

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   "new content",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new content" {
		t.Errorf("expected overwrite, got: %q", string(data))
	}
}

func TestBuiltinWrite_RelativePath(t *testing.T) {
	dir := t.TempDir()

	result := builtinWrite(context.Background(), map[string]any{
		"file_path": "relative.txt",
		"content":   "relative write",
	}, dir)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, err := os.ReadFile(filepath.Join(dir, "relative.txt"))
	if err != nil {
		t.Fatalf("file not found: %s", err)
	}
	if string(data) != "relative write" {
		t.Errorf("content mismatch: %q", string(data))
	}
}

func TestBuiltinWrite_MissingFilePath(t *testing.T) {
	result := builtinWrite(context.Background(), map[string]any{
		"content": "orphan content",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for missing file_path")
	}
}

func TestBuiltinWrite_MissingContent(t *testing.T) {
	result := builtinWrite(context.Background(), map[string]any{
		"file_path": "/tmp/test.txt",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for missing content")
	}
}

func TestBuiltinBash_SimpleCommand(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo hello",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	stdout := out["stdout"].(string)
	if strings.TrimSpace(stdout) != "hello" {
		t.Errorf("expected 'hello', got %q", stdout)
	}
	if int(out["exit_code"].(float64)) != 0 {
		t.Errorf("expected exit code 0, got %v", out["exit_code"])
	}
}

func TestBuiltinBash_NonZeroExit(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "exit 42",
	}, "")

	if !result.IsError {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(result.Error, "exit code 42") {
		t.Errorf("expected exit code 42 in error, got: %s", result.Error)
	}
}

func TestBuiltinBash_WorkDir(t *testing.T) {
	dir := t.TempDir()

	result := builtinBash(context.Background(), map[string]any{
		"command": "pwd",
	}, dir)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	stdout := strings.TrimSpace(out["stdout"].(string))

	// Resolve symlinks for comparison (macOS /var -> /private/var)
	resolvedDir, _ := filepath.EvalSymlinks(dir)
	resolvedOut, _ := filepath.EvalSymlinks(stdout)

	if resolvedOut != resolvedDir {
		t.Errorf("expected pwd %q, got %q", resolvedDir, resolvedOut)
	}
}

func TestBuiltinBash_StderrCapture(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo stderr_msg >&2; echo stdout_msg",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)

	if !strings.Contains(out["stdout"].(string), "stdout_msg") {
		t.Error("expected stdout to contain 'stdout_msg'")
	}
	if !strings.Contains(out["stderr"].(string), "stderr_msg") {
		t.Error("expected stderr to contain 'stderr_msg'")
	}
}

func TestBuiltinBash_MissingCommand(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing command")
	}
}

func TestBuiltinBash_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := builtinBash(ctx, map[string]any{
		"command": "sleep 10",
	}, "")

	if !result.IsError {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Errorf("expected timeout message, got: %s", result.Error)
	}
}

func TestBuiltinBash_PipedCommand(t *testing.T) {
	result := builtinBash(context.Background(), map[string]any{
		"command": "echo 'hello world' | wc -w",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	stdout := strings.TrimSpace(out["stdout"].(string))
	if stdout != "2" {
		t.Errorf("expected word count '2', got %q", stdout)
	}
}

// Test that builtins are dispatched correctly through the executor.
func TestExecutor_BuiltinDispatch(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ReadDef)

	exec := NewExecutor(reg, "")

	// Read a file that doesn't exist — should get a clean error, not a binary-not-found error
	result := exec.Execute(context.Background(), "read", map[string]any{
		"file_path": "/nonexistent/file.txt",
	})

	if !result.IsError {
		t.Fatal("expected error")
	}
	if strings.Contains(result.Error, "not found on PATH") {
		t.Error("builtin should not check PATH — got binary-not-found error")
	}
	if !strings.Contains(result.Error, "failed to read file") {
		t.Errorf("expected read error, got: %s", result.Error)
	}
}

func TestBuiltins_AllRegistered(t *testing.T) {
	reg := DefaultRegistry()

	for _, name := range []string{"read", "write", "bash"} {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("builtin %q not found in default registry", name)
			continue
		}
		if !def.IsBuiltinTool() {
			t.Errorf("expected %q to be a builtin tool", name)
		}
		if def.InputSchema == nil {
			t.Errorf("builtin %q has nil InputSchema", name)
		}
	}
}

func TestCheckBinaries_SkipsBuiltins(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{
		Name:    "builtin_test",
		Builtin: func(ctx context.Context, input map[string]any, workDir string) Result { return Result{} },
	})
	reg.Register(ToolDef{
		Name:   "missing_binary",
		Binary: "nonexistent-binary-xyz",
	})

	missing := reg.CheckBinaries()

	if _, ok := missing["builtin_test"]; ok {
		t.Error("CheckBinaries should skip builtin tools")
	}
	if _, ok := missing["missing_binary"]; !ok {
		t.Error("CheckBinaries should report missing subprocess binary")
	}
}

// Integration: read -> write -> read roundtrip
func TestBuiltins_ReadWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.txt")
	content := "line 1\nline 2\nline 3\n"

	// Write
	writeResult := builtinWrite(context.Background(), map[string]any{
		"file_path": path,
		"content":   content,
	}, "")
	if writeResult.IsError {
		t.Fatalf("write failed: %s", writeResult.Error)
	}

	// Read back
	readResult := builtinRead(context.Background(), map[string]any{
		"file_path": path,
	}, "")
	if readResult.IsError {
		t.Fatalf("read failed: %s", readResult.Error)
	}

	var out map[string]any
	json.Unmarshal([]byte(readResult.Output), &out)
	readContent := out["content"].(string)

	// Verify all lines present with line numbers
	if !strings.Contains(readContent, "line 1") {
		t.Error("roundtrip: missing 'line 1'")
	}
	if !strings.Contains(readContent, "line 3") {
		t.Error("roundtrip: missing 'line 3'")
	}
}
