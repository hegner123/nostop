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
// ReadTracker unit tests
// =============================================================================

func TestReadTracker_RecordAndCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	rt := NewReadTracker()

	// Before read: write should fail (file exists, never read)
	if err := rt.CheckWrite(path); err == nil {
		t.Fatal("expected error: existing file was never read")
	}

	// Record read
	if err := rt.RecordRead(path); err != nil {
		t.Fatalf("RecordRead failed: %s", err)
	}

	// After read: write should succeed (file unchanged)
	if err := rt.CheckWrite(path); err != nil {
		t.Fatalf("unexpected error after read: %s", err)
	}
}

func TestReadTracker_NewFileBypassesCheck(t *testing.T) {
	rt := NewReadTracker()

	// Non-existent file — write should be allowed without prior read
	path := filepath.Join(t.TempDir(), "newfile.txt")
	if err := rt.CheckWrite(path); err != nil {
		t.Fatalf("new file should not require read: %s", err)
	}
}

func TestReadTracker_ModifiedAfterRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("original"), 0644)

	rt := NewReadTracker()
	rt.RecordRead(path)

	// Modify the file (change content and mtime)
	time.Sleep(10 * time.Millisecond) // ensure mtime changes
	os.WriteFile(path, []byte("modified externally"), 0644)

	// Write should fail — file was modified since last read
	err := rt.CheckWrite(path)
	if err == nil {
		t.Fatal("expected error: file modified after read")
	}
	if !strings.Contains(err.Error(), "modified after last read") {
		t.Errorf("unexpected error message: %s", err)
	}
}

func TestReadTracker_RereadAfterModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("v1"), 0644)

	rt := NewReadTracker()
	rt.RecordRead(path)

	// External modification
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("v2"), 0644)

	// Should fail
	if err := rt.CheckWrite(path); err == nil {
		t.Fatal("expected stale write error")
	}

	// Re-read to pick up new mtime
	rt.RecordRead(path)

	// Should succeed now
	if err := rt.CheckWrite(path); err != nil {
		t.Fatalf("re-read should allow write: %s", err)
	}
}

func TestReadTracker_RecordWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("initial"), 0644)

	rt := NewReadTracker()
	rt.RecordRead(path)

	// Simulate a write (update the file)
	os.WriteFile(path, []byte("updated by us"), 0644)
	rt.RecordWrite(path)

	// Second write should succeed without another read
	// because RecordWrite updated the tracked mtime
	if err := rt.CheckWrite(path); err != nil {
		t.Fatalf("write after RecordWrite should succeed: %s", err)
	}
}

func TestReadTracker_WasRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	rt := NewReadTracker()

	if rt.WasRead(path) {
		t.Error("file should not be marked as read initially")
	}

	rt.RecordRead(path)

	if !rt.WasRead(path) {
		t.Error("file should be marked as read after RecordRead")
	}
}

func TestReadTracker_Clear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	rt := NewReadTracker()
	rt.RecordRead(path)

	if rt.Len() != 1 {
		t.Errorf("expected 1 tracked file, got %d", rt.Len())
	}

	rt.Clear()

	if rt.Len() != 0 {
		t.Errorf("expected 0 tracked files after clear, got %d", rt.Len())
	}
	if rt.WasRead(path) {
		t.Error("file should not be marked as read after clear")
	}
}

func TestReadTracker_NonexistentFileRecordRead(t *testing.T) {
	rt := NewReadTracker()
	err := rt.RecordRead("/nonexistent/file.txt")
	if err == nil {
		t.Fatal("RecordRead should fail for nonexistent file")
	}
}

func TestReadTracker_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	rt := NewReadTracker()

	// Create files
	for i := range 20 {
		path := filepath.Join(dir, strings.Replace("file_NN.txt", "NN", toString(i), 1))
		os.WriteFile(path, []byte("content"), 0644)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)

	// Concurrent reads
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path := filepath.Join(dir, strings.Replace("file_NN.txt", "NN", toString(idx), 1))
			if err := rt.RecordRead(path); err != nil {
				errs <- err
			}
		}(i)
	}

	// Concurrent checks
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path := filepath.Join(dir, strings.Replace("file_NN.txt", "NN", toString(idx), 1))
			rt.CheckWrite(path) // may or may not error depending on timing
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %s", err)
	}

	if rt.Len() != 20 {
		t.Errorf("expected 20 tracked files, got %d", rt.Len())
	}
}

// =============================================================================
// Context integration tests
// =============================================================================

func TestReadTrackerContext_RoundTrip(t *testing.T) {
	rt := NewReadTracker()
	ctx := ContextWithReadTracker(context.Background(), rt)

	got := ReadTrackerFromContext(ctx)
	if got != rt {
		t.Error("expected same tracker from context")
	}
}

func TestReadTrackerContext_MissingReturnsNil(t *testing.T) {
	got := ReadTrackerFromContext(context.Background())
	if got != nil {
		t.Error("expected nil tracker from bare context")
	}
}

// =============================================================================
// Full executor-mediated flow tests
// =============================================================================

func TestExecutorFlow_ReadThenWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("original"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Step 1: Read the file
	readResult := exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})
	if readResult.IsError {
		t.Fatalf("read failed: %s", readResult.Error)
	}

	// Step 2: Write should succeed (file read, not modified)
	writeResult := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "updated content",
	})
	if writeResult.IsError {
		t.Fatalf("write after read should succeed: %s", writeResult.Error)
	}

	// Verify content
	data, _ := os.ReadFile(path)
	if string(data) != "updated content" {
		t.Errorf("expected 'updated content', got %q", string(data))
	}
}

func TestExecutorFlow_WriteWithoutRead_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("original"), 0644)

	reg := NewRegistry()
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Write without reading first — should fail for existing file
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "blind overwrite",
	})

	if !result.IsError {
		t.Fatal("expected error: writing existing file without reading")
	}
	if !strings.Contains(result.Error, "must be read before writing") {
		t.Errorf("unexpected error message: %s", result.Error)
	}

	// Original content should be preserved
	data, _ := os.ReadFile(path)
	if string(data) != "original" {
		t.Errorf("original content should be preserved, got %q", string(data))
	}
}

func TestExecutorFlow_WriteNewFile_NoReadRequired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "brand_new.txt")

	reg := NewRegistry()
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Write to a file that doesn't exist — should work without read
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "new file content",
	})

	if result.IsError {
		t.Fatalf("creating new file should not require read: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new file content" {
		t.Errorf("expected 'new file content', got %q", string(data))
	}
}

func TestExecutorFlow_StaleWrite_ExternalModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("v1"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Read
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})

	// External modification (simulates another process editing the file)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("v2 by someone else"), 0644)

	// Write should fail — file changed since read
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "v3 blind overwrite",
	})

	if !result.IsError {
		t.Fatal("expected stale write error")
	}
	if !strings.Contains(result.Error, "modified after last read") {
		t.Errorf("unexpected error: %s", result.Error)
	}

	// Original v2 content should be preserved
	data, _ := os.ReadFile(path)
	if string(data) != "v2 by someone else" {
		t.Errorf("v2 content should be preserved, got %q", string(data))
	}
}

func TestExecutorFlow_RereadFixesStaleWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("v1"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Read v1
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})

	// External modification
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("v2"), 0644)

	// Write fails (stale)
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "v3",
	})
	if !result.IsError {
		t.Fatal("expected stale write error")
	}

	// Re-read
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})

	// Now write should succeed
	result = exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "v3",
	})
	if result.IsError {
		t.Fatalf("write after re-read should succeed: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "v3" {
		t.Errorf("expected 'v3', got %q", string(data))
	}
}

func TestExecutorFlow_ConsecutiveWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("v1"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Read once
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})

	// First write
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "v2",
	})
	if result.IsError {
		t.Fatalf("first write failed: %s", result.Error)
	}

	// Second write without re-reading — should succeed because
	// RecordWrite updated the tracker after the first write
	result = exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "v3",
	})
	if result.IsError {
		t.Fatalf("second write should succeed (RecordWrite tracks it): %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "v3" {
		t.Errorf("expected 'v3', got %q", string(data))
	}
}

func TestExecutorFlow_CreateThenOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "created.txt")

	reg := NewRegistry()
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Create (no read needed)
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "created",
	})
	if result.IsError {
		t.Fatalf("create failed: %s", result.Error)
	}

	// Overwrite (should succeed — RecordWrite tracked the creation)
	result = exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "overwritten",
	})
	if result.IsError {
		t.Fatalf("overwrite after create should succeed: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "overwritten" {
		t.Errorf("expected 'overwritten', got %q", string(data))
	}
}

func TestExecutorFlow_ReadTrackerIsolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)

	// Two executors should have independent trackers
	exec1 := NewExecutor(reg, "")
	exec2 := NewExecutor(reg, "")

	// exec1 reads
	exec1.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})

	// exec1 can write
	result := exec1.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "from exec1",
	})
	if result.IsError {
		t.Fatalf("exec1 write should succeed: %s", result.Error)
	}

	// exec2 has NOT read — should fail
	result = exec2.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "from exec2",
	})
	if !result.IsError {
		t.Fatal("exec2 should fail: file not read by exec2")
	}
}

func TestExecutorFlow_ReadViaOffset_StillRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Partial read (offset + limit)
	readResult := exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
		"offset":    float64(2),
		"limit":     float64(1),
	})
	if readResult.IsError {
		t.Fatalf("partial read failed: %s", readResult.Error)
	}

	// Write should succeed — any read (even partial) records the file
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "replaced",
	})
	if result.IsError {
		t.Fatalf("write after partial read should succeed: %s", result.Error)
	}
}

func TestExecutorFlow_BashModifiesFile_WriteFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("original"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	reg.Register(BashDef)
	exec := NewExecutor(reg, "")

	// Read the file
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})

	// Bash modifies the file (simulates a build tool, formatter, etc.)
	time.Sleep(10 * time.Millisecond)
	exec.Execute(context.Background(), "bash", map[string]any{
		"command": "echo 'modified by bash' > " + path,
	})

	// Write should fail — bash changed the file's mtime
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "blind overwrite after bash",
	})
	if !result.IsError {
		t.Fatal("expected stale write error after bash modification")
	}
	if !strings.Contains(result.Error, "modified after last read") {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestExecutorFlow_WriteErrorOutput_IsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	reg := NewRegistry()
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Write without read — check that error is usable
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "blind",
	})

	if !result.IsError {
		t.Fatal("expected error")
	}
	// Error message should mention the file path and what to do
	if !strings.Contains(result.Error, path) {
		t.Errorf("error should mention file path: %s", result.Error)
	}
	if !strings.Contains(result.Error, "read") {
		t.Errorf("error should suggest reading: %s", result.Error)
	}
}

func TestExecutorFlow_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	os.WriteFile(fileA, []byte("aaa"), 0644)
	os.WriteFile(fileB, []byte("bbb"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	// Read only file A
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": fileA,
	})

	// Write A should succeed
	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": fileA,
		"content":   "aaa updated",
	})
	if result.IsError {
		t.Fatalf("write A should succeed: %s", result.Error)
	}

	// Write B should fail — never read
	result = exec.Execute(context.Background(), "write", map[string]any{
		"file_path": fileB,
		"content":   "bbb updated",
	})
	if !result.IsError {
		t.Fatal("write B should fail: never read")
	}

	// Read B, then write should succeed
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": fileB,
	})
	result = exec.Execute(context.Background(), "write", map[string]any{
		"file_path": fileB,
		"content":   "bbb updated",
	})
	if result.IsError {
		t.Fatalf("write B after read should succeed: %s", result.Error)
	}
}

func TestExecutorFlow_TrackerState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	reg := NewRegistry()
	reg.Register(ReadDef)
	exec := NewExecutor(reg, "")

	// Tracker starts empty
	if exec.ReadTracker().Len() != 0 {
		t.Error("tracker should start empty")
	}

	// After read, tracker has one entry
	exec.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})
	if exec.ReadTracker().Len() != 1 {
		t.Errorf("expected 1 tracked file, got %d", exec.ReadTracker().Len())
	}

	if !exec.ReadTracker().WasRead(path) {
		t.Error("file should be marked as read")
	}
}

// Verify that the write result JSON still looks right
func TestExecutorFlow_WriteResultFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	reg := NewRegistry()
	reg.Register(WriteDef)
	exec := NewExecutor(reg, "")

	result := exec.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "hello",
	})

	if result.IsError {
		t.Fatalf("write failed: %s", result.Error)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("output not valid JSON: %s", err)
	}
	if out["status"] != "ok" {
		t.Errorf("expected status ok, got %v", out["status"])
	}
	if int(out["bytes_written"].(float64)) != 5 {
		t.Errorf("expected 5 bytes written, got %v", out["bytes_written"])
	}
}
