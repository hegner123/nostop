package tools

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// UTF8 builtin tests
// =============================================================================

func TestUTF8_AlreadyUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.txt")
	os.WriteFile(path, []byte("Hello, world!\n"), 0644)

	result := builtinUTF8(context.Background(), map[string]any{
		"file": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out utf8Result
	json.Unmarshal([]byte(result.Output), &out)

	if out.Status != "already_utf8" {
		t.Errorf("expected status already_utf8, got %q", out.Status)
	}
	if out.Detected != utf8EncUTF8 {
		t.Errorf("expected detected utf8, got %q", out.Detected)
	}
}

func TestUTF8_UTF8BOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	bom := []byte{0xEF, 0xBB, 0xBF}
	os.WriteFile(path, append(bom, []byte("Hello BOM\n")...), 0644)

	result := builtinUTF8(context.Background(), map[string]any{
		"file":   path,
		"backup": false,
	}, "")

	var out utf8Result
	json.Unmarshal([]byte(result.Output), &out)

	if out.Status != "converted" {
		t.Errorf("expected converted, got %q", out.Status)
	}
	if out.Detected != utf8EncUTF8BOM {
		t.Errorf("expected detected utf8_bom, got %q", out.Detected)
	}

	// File should have BOM stripped
	data, _ := os.ReadFile(path)
	if data[0] == 0xEF {
		t.Error("BOM should have been stripped")
	}
}

func TestUTF8_NullLaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nulls.txt")
	// Null-laced ASCII: 'H' 0x00 'i' 0x00
	os.WriteFile(path, []byte{'H', 0, 'i', 0, '\n', 0}, 0644)

	result := builtinUTF8(context.Background(), map[string]any{
		"file":   path,
		"backup": false,
	}, "")

	var out utf8Result
	json.Unmarshal([]byte(result.Output), &out)

	if out.Status != "converted" {
		t.Errorf("expected converted, got %q", out.Status)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "\x00") {
		t.Error("null bytes should have been stripped")
	}
}

func TestUTF8_UTF16LE(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "utf16le.txt")

	// UTF-16 LE BOM + "Hi"
	var content []byte
	content = append(content, 0xFF, 0xFE) // BOM
	buf := make([]byte, 2)
	for _, r := range "Hi\n" {
		binary.LittleEndian.PutUint16(buf, uint16(r))
		content = append(content, buf...)
	}
	os.WriteFile(path, content, 0644)

	result := builtinUTF8(context.Background(), map[string]any{
		"file":   path,
		"backup": false,
	}, "")

	var out utf8Result
	json.Unmarshal([]byte(result.Output), &out)

	if out.Status != "converted" {
		t.Fatalf("expected converted, got %q", out.Status)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Hi") {
		t.Errorf("expected 'Hi' in converted output, got %q", string(data))
	}
}

func TestUTF8_Backup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	bom := []byte{0xEF, 0xBB, 0xBF}
	os.WriteFile(path, append(bom, []byte("content")...), 0644)

	result := builtinUTF8(context.Background(), map[string]any{
		"file":   path,
		"backup": true,
	}, "")

	var out utf8Result
	json.Unmarshal([]byte(result.Output), &out)

	if out.Backup == "" {
		t.Error("expected backup path")
	}

	// Backup should exist
	if _, err := os.Stat(out.Backup); err != nil {
		t.Errorf("backup file should exist: %s", err)
	}
}

func TestUTF8_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, []byte{}, 0644)

	result := builtinUTF8(context.Background(), map[string]any{
		"file": path,
	}, "")

	var out utf8Result
	json.Unmarshal([]byte(result.Output), &out)

	if out.Status != "already_utf8" {
		t.Errorf("empty file should be already_utf8, got %q", out.Status)
	}
}

func TestUTF8_MissingFile(t *testing.T) {
	result := builtinUTF8(context.Background(), map[string]any{
		"file": "/nonexistent/file.txt",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestUTF8_MissingParam(t *testing.T) {
	result := builtinUTF8(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing file param")
	}
}

// =============================================================================
// Imports builtin tests
// =============================================================================

func TestImports_GoFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"fmt"
	"os"
)

func main() {}
`), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.FilesScanned != 1 {
		t.Errorf("expected 1 file scanned, got %d", out.FilesScanned)
	}
	if len(out.Files) != 1 {
		t.Fatalf("expected 1 file with imports, got %d", len(out.Files))
	}
	if out.Files[0].Language != "go" {
		t.Errorf("expected language go, got %q", out.Files[0].Language)
	}
	if len(out.Files[0].Imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(out.Files[0].Imports))
	}

	// Check package reverse index
	if _, ok := out.Packages["fmt"]; !ok {
		t.Error("expected 'fmt' in packages map")
	}
}

func TestImports_PythonFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(`import os
import sys
from pathlib import Path
from .local import helper
`), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	if len(out.Files[0].Imports) != 4 {
		t.Errorf("expected 4 imports, got %d", len(out.Files[0].Imports))
	}

	// Check relative import detection
	for _, imp := range out.Files[0].Imports {
		if imp.Package == ".local" && imp.Type != "relative" {
			t.Errorf("expected .local to be relative, got %q", imp.Type)
		}
	}
}

func TestImports_JSFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.ts"), []byte(`import React from 'react';
import { useState } from 'react';
import './styles.css';
const path = require('path');
`), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	if len(out.Files) == 0 {
		t.Fatal("expected imports from TS file")
	}
	if out.Files[0].Language != "typescript" {
		t.Errorf("expected language typescript, got %q", out.Files[0].Language)
	}
}

func TestImports_ExtFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nimport \"fmt\"\n"), 0644)
	os.WriteFile(filepath.Join(dir, "b.py"), []byte("import os\n"), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir": dir,
		"ext": ".go",
	}, "")

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.FilesScanned != 1 {
		t.Errorf("expected 1 file scanned with ext filter, got %d", out.FilesScanned)
	}
}

func TestImports_Recursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nimport \"fmt\"\n"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("package sub\nimport \"os\"\n"), 0644)

	// Non-recursive: only root
	r1 := builtinImports(context.Background(), map[string]any{
		"dir": dir,
	}, "")
	var out1 importsResult
	json.Unmarshal([]byte(r1.Output), &out1)

	if out1.FilesScanned != 1 {
		t.Errorf("non-recursive: expected 1 file, got %d", out1.FilesScanned)
	}

	// Recursive: both
	r2 := builtinImports(context.Background(), map[string]any{
		"dir":       dir,
		"recursive": true,
	}, "")
	var out2 importsResult
	json.Unmarshal([]byte(r2.Output), &out2)

	if out2.FilesScanned != 2 {
		t.Errorf("recursive: expected 2 files, got %d", out2.FilesScanned)
	}
}

func TestImports_SkipDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("import 'x';"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "app.js"), []byte("import 'y';"), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir":       dir,
		"recursive": true,
	}, "")

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	for _, f := range out.Files {
		if strings.Contains(f.Path, "node_modules") {
			t.Error("node_modules should be skipped")
		}
	}
}

func TestImports_CImports(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.c"), []byte(`#include <stdio.h>
#include "local.h"
`), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	if len(out.Files[0].Imports) != 2 {
		t.Errorf("expected 2 C imports, got %d", len(out.Files[0].Imports))
	}

	for _, imp := range out.Files[0].Imports {
		if imp.Package == "stdio.h" && imp.Type != "system" {
			t.Errorf("expected stdio.h to be system, got %q", imp.Type)
		}
		if imp.Package == "local.h" && imp.Type != "local" {
			t.Errorf("expected local.h to be local, got %q", imp.Type)
		}
	}
}

func TestImports_Summary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nimport \"fmt\"\n"), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if !strings.Contains(out.Summary, "1 go") {
		t.Errorf("summary should mention '1 go', got %q", out.Summary)
	}
}

func TestImports_NonexistentDir(t *testing.T) {
	result := builtinImports(context.Background(), map[string]any{
		"dir": "/nonexistent/dir",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestImports_MissingParam(t *testing.T) {
	result := builtinImports(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing dir")
	}
}

func TestImports_GoModuleDetection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.22\n"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"fmt"
	"example.com/myapp/internal/pkg"
	"github.com/external/lib"
)
`), 0644)

	result := builtinImports(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	var out importsResult
	json.Unmarshal([]byte(result.Output), &out)

	imports := out.Files[0].Imports
	for _, imp := range imports {
		switch imp.Package {
		case "fmt":
			if imp.Type != "stdlib" {
				t.Errorf("fmt should be stdlib, got %q", imp.Type)
			}
		case "example.com/myapp/internal/pkg":
			if imp.Type != "local" {
				t.Errorf("local module import should be local, got %q", imp.Type)
			}
		case "github.com/external/lib":
			if imp.Type != "external" {
				t.Errorf("github import should be external, got %q", imp.Type)
			}
		}
	}
}

// =============================================================================
// Registry integration
// =============================================================================

func TestPhase5_BuiltinsInRegistry(t *testing.T) {
	reg := DefaultRegistry()

	for _, name := range []string{"utf8", "imports"} {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("%q not found in default registry", name)
			continue
		}
		if !def.IsBuiltinTool() {
			t.Errorf("%q should be a builtin", name)
		}
	}
}

func TestPhase5_TotalToolCount(t *testing.T) {
	reg := DefaultRegistry()
	// 3 subprocess (sig, errs, transform) + 15 builtins
	if reg.Len() != 18 {
		t.Errorf("expected 18 tools, got %d", reg.Len())
		t.Logf("tools: %v", reg.Names())
	}
}
