# Plan: Reimplement Terse-MCP Tools as Native Builtins

## Goal

Replace all 15 subprocess-based tool definitions with in-process Go implementations inside `internal/tools/`. When complete, the `nostop` binary is fully self-contained — no external tool binaries required.

## Current State

- 15 tools defined in `internal/tools/definitions.go` as subprocess tools (`Binary` + `FlagMap`)
- 3 builtins already implemented: `read`, `write`, `bash` (in `builtins.go`)
- Executor dispatches builtins via `BuiltinFunc`, subprocesses via `executeSubprocess`
- All tools share the same `ToolDef` type with `InputSchema` (JSON Schema for the API)

## Complexity Analysis

Source: `$HOME/Documents/Code/Go_dev/terse-mcp/`

| Phase | Tool | Core LOC | Shells Out? | Key Dependencies | Difficulty |
|-------|------|----------|-------------|------------------|------------|
| 1 | tabcount | ~200 | No | bufio | Low |
| 1 | notab | ~280 | No | strings, os | Low |
| 1 | delete | ~250 | No | os, filepath | Low |
| 2 | split | ~350 | No | bufio, filepath, sort | Medium |
| 2 | splice | ~300 | No | bufio, filepath | Medium |
| 2 | stump | ~250 | Yes (stump-core) | os, filepath | Medium (rewrite as pure Go walk) |
| 3 | checkfor | ~900 | No | strings, bufio, filepath | High |
| 3 | conflicts | ~400 | No | strings, bufio | Medium |
| 4 | repfor | ~1200 | No | strings, bufio, atomic I/O | High |
| 4 | cleanDiff | ~400 | Yes (git) | exec (git only), strings | Medium |
| 5 | utf8 | ~400 | No | unicode/utf8, unicode/utf16, encoding/binary | Medium |
| 5 | imports | ~950 | No | strings, filepath, regexp | High (11 language parsers) |
| 6 | sig | ~3500 | No | go/ast, go/parser, regexp | Very High (3 language extractors) |
| 6 | transform | ~600 | Yes (shell exec) | json, sort, exec | High (pipeline engine) |
| 6 | errs | ~1200 | No | strings, strconv | High (7 format parsers) |

**Total estimated LOC: ~10,400**

## Implementation Phases

Each phase is a self-contained unit of work. The tool tests from `executor_test.go`, `builtins_test.go`, and `edge_test.go` continue passing throughout — each phase adds builtins and removes the corresponding subprocess definitions.

### Phase 1: Trivial Tools (tabcount, notab, delete)

**Estimated effort: ~500 LOC implementation + ~300 LOC tests**

These three have the simplest logic and no external dependencies.

**tabcount** — Count leading tabs per line in a file.
- Input: `file`, `start_line`, `end_line`
- Logic: Read file line by line, count leading `\t` chars, filter by line range.
- Output: JSON array of `{line, depth}` objects.

**notab** — Convert tabs to spaces (or spaces to tabs).
- Input: `file`, `spaces` (int), `tabs` (bool for inverse mode)
- Logic: Read file, replace tabs with N spaces (or leading spaces with tabs). Write back. Count replacements.
- Output: `{replacements, lines_affected, direction}`

**delete** — Move file/directory to `~/.Trash`.
- Input: `path` (absolute)
- Logic: Validate path (block system dirs: `/System`, `/usr`, `/bin`, `/sbin`, `/Library`). `os.Rename` to `~/.Trash/<name>`. Handle collisions with timestamp suffix. For directories, use `os.Rename` (atomic on same filesystem).
- Output: `{original_path, trash_path, type, size, items}`

**File structure:**
```
internal/tools/builtin_tabcount.go
internal/tools/builtin_notab.go
internal/tools/builtin_delete.go
```

**Verification:** Remove `TabcountDef`, `NotabDef`, `DeleteDef` from `AllTools()`. Add new builtin defs to `BuiltinTools()`. Run `go test ./internal/tools/`. Integration test: call each via executor, verify JSON output matches the MCP tool's output format.

### Phase 2: File Operations (split, splice, stump)

**Estimated effort: ~700 LOC implementation + ~400 LOC tests**

**split** — Split a file at specified line numbers.
- Input: `file`, `lines` (array of ints)
- Logic: Read file, sort split points, write sequential output files (`_001`, `_002`, etc.). Handle edge cases: split point beyond EOF, empty segments, zero-padded naming.
- Output: `{source, parts: [{file, start_line, end_line, lines}]}`
- Note: Original tool uses temp files for atomic writes. Reimplement with `os.CreateTemp` + `os.Rename`.

**splice** — Insert file contents into target file.
- Input: `source`, `target`, `mode` (append/prepend/replace/insert), `line` (for insert mode)
- Logic: Read source. For append/prepend, read target + concatenate. For replace, overwrite. For insert, split target at line N, sandwich source between halves.
- Output: `{source, target, mode, lines_inserted, total_lines}`
- Note: Preserve file permissions. Use atomic write (temp + rename).

**stump** — Directory tree visualization.
- Input: `dir`, `depth`, `include_ext`, `exclude_ext`, `exclude_patterns`, `show_size`, `show_hidden`
- Logic: `os.ReadDir` recursive walk. Apply filters. Build JSON tree. This replaces the external `stump-core` binary entirely.
- Output: `{root, depth, stats: {dirs, files}, tree: [{path, type, size?}]}`
- Note: The MCP stump tool already shells out to `stump-core`. This phase eliminates that dependency.

### Phase 3: Search & Parse (checkfor, conflicts)

**Estimated effort: ~1000 LOC implementation + ~500 LOC tests**

**checkfor** — Search files for exact string matches.
- Input: `search`, `dir` (array), `file` (array), `ext`, `case_insensitive`, `whole_word`, `context`, `exclude`
- Logic: Walk directories (single-depth by default). Read each file line by line. Match search string (with case/word options). Collect context lines before/after. Apply exclude filters.
- Key complexity: Multi-line search support (`\n` in search string spans lines). This requires a sliding window or buffer approach rather than line-by-line.
- Output: `{search, matches: [{file, line, content, context_before, context_after}], total_matches, files_searched}`
- Reference: `$HOME/Documents/Code/Go_dev/terse-mcp/checkfor/main.go`

**conflicts** — Parse git merge conflict markers.
- Input: `file` (array), `context_lines`
- Logic: Read files, scan for `<<<<<<<`, `=======`, `>>>>>>>` markers. Support diff3 style (`|||||||`). Extract ours/theirs/base content, line numbers, refs, surrounding context.
- Output: `{files: [{file, conflicts: [{line, end_line, ours_ref, theirs_ref, ours, theirs, base?, context_above, context_below}]}], total, has_diff3}`

### Phase 4: Replace & Diff (repfor, cleanDiff)

**Estimated effort: ~1200 LOC implementation + ~600 LOC tests**

**repfor** — Search and replace strings across files.
- Input: `search`, `replace`, `dir`, `file`, `ext`, `case_insensitive`, `whole_word`, `dry_run`, `recursive`, `exclude`
- Logic: Shares file-walking logic with checkfor. For each match, perform replacement. Write back atomically (temp file + rename to preserve permissions). Dry-run mode returns would-be changes without writing.
- Key complexity: Multi-line replacement (`\n` in both search and replace). Four modes: exact, case-insensitive, whole-word, case-insensitive+whole-word. Exclude filters apply to lines, not files.
- Output: `{search, replace, files_modified, total_replacements, changes: [{file, replacements, lines_changed}]}`
- Note: Consider extracting shared file-walking logic into a `filewalk.go` helper used by both checkfor and repfor.

**cleanDiff** — Compact git diff as structured JSON.
- Input: `path`, `ref`, `staged`, `stat_only`, `context_lines`, `file_filter`
- Logic: Build `git diff` command from params. Execute via `exec.Command` (git is an external dependency we can't avoid). Parse unified diff output: extract file names, hunk headers (`@@ -a,b +c,d @@`), added/removed lines.
- Output: `{summary: {files_changed, insertions, deletions}, files: [{path, status, hunks: [{old_start, new_start, added, removed, lines}]}]}`
- Note: This tool inherently depends on `git`. The builtin just wraps the `git diff` call + parses output. No `cleanDiff` binary needed.

### Phase 5: Encoding & Imports (utf8, imports)

**Estimated effort: ~1100 LOC implementation + ~500 LOC tests**

**utf8** — Fix corrupted UTF-16 files to clean UTF-8.
- Input: `file`, `backup` (bool)
- Logic: Read raw bytes. Detect encoding via BOM analysis and null-byte patterns. If UTF-16LE/BE: decode via `unicode/utf16`. Handle mixed encoding, unpaired surrogates. Create `.bak` backup if requested. Write clean UTF-8.
- Output: `{file, original_encoding, had_bom, fixed, bytes_before, bytes_after, backup_path?}`

**imports** — Map imports/dependencies across a directory.
- Input: `dir`, `ext`, `recursive`
- Logic: Walk directory. For each file, detect language by extension. Parse imports with language-specific logic:
  - **Go:** `go/parser` for import blocks
  - **Python:** regex for `import X` / `from X import Y`
  - **JS/TS:** regex for `import ... from` / `require(...)`
  - **Zig:** regex for `@import(...)`
  - **Rust:** regex for `use X` / `mod X`
  - **C/C++:** regex for `#include`
  - **Swift:** regex for `import X`
  - **Java/Kotlin:** regex for `import X`
  - **Ruby:** regex for `require` / `require_relative`
  - **Shell:** regex for `source` / `.`
- Classify imports: stdlib, external, local, relative, system.
- Build reverse index: which packages are used by which files.
- Output: `{files: [{path, language, imports: [{package, type, line}]}], packages: {pkg: [files]}, summary}`
- Note: Go imports should use `go/parser` for accuracy. Other languages use regex, which is sufficient for import statements.

### Phase 6: Complex Tools (sig, transform, errs)

**Estimated effort: ~2500 LOC implementation + ~800 LOC tests**

These are the hardest and can be split into sub-phases.

**sig** — Extract API surface from source files.
- Input: `file`, `all` (include private)
- Logic: Three extractors, one per language:
  - **Go extractor** (~200 LOC): Use `go/parser` + `go/ast` to extract function signatures, type definitions (struct/interface), const/var blocks. Filter by exported/unexported. This is the most reliable extractor.
  - **TypeScript extractor** (~400 LOC): Regex-based parsing of `.ts`/`.tsx` files. Extract: `export function`, `export class`, `export interface`, `export type`, `export const/let`. Handle generics, decorators.
  - **C# extractor** (~500 LOC): Regex-based parsing of `.cs` files. Extract: `class`, `interface`, `struct`, `enum`, `record`, `delegate`, method signatures. Handle namespaces, access modifiers.
- Output: `{file, package, imports, types: [...], functions: [...], constants: [...], variables: [...]}`
- Sub-phase 6a: Go extractor only (most useful for this project)
- Sub-phase 6b: TypeScript extractor
- Sub-phase 6c: C# extractor

**transform** — Composable JSON array pipeline.
- Input: `exec` (shell command) or `file`, `pipeline` (array of operations)
- Logic: Get input data (run shell command or read file). Parse JSON array. Apply pipeline operations in order:
  - `group_by(key)` — group objects by field value
  - `sort_by(key, desc)` — sort by field
  - `filter(key, eq/neq/contains/exists)` — filter objects
  - `count(key?)` — count items or count by field
  - `flatten` — flatten nested arrays
  - `format(template)` — string template with `{field}` placeholders
- Support dot-path navigation (`milestone.title`) for nested objects.
- Output: The pipeline result (varies by operations).
- Note: `exec` mode needs `exec.Command` — this is an intentional external call (user-specified command), not a tool dependency.

**errs** — Compact error/lint parser.
- Input: `input` (raw error text), `format` (hint)
- Logic: Seven format parsers + auto-detection:
  - Go (`go build`, `go vet`, `golangci-lint`)
  - GCC/Clang/Swift (colon-separated `file:line:col: error:`)
  - Rust (`rustc`, `clippy`)
  - TypeScript (`tsc`)
  - ESLint (stylish format)
  - dotnet/C# (Roslyn diagnostics)
  - Python (`flake8`, `mypy`)
  - Kotlin
- Strip ANSI codes, normalize output, deduplicate.
- Output: `{errors: [{file, line, col, code, severity, message}], format, count, files, summary}`

## File Organization

```
internal/tools/
├── registry.go          # ToolDef, Registry (unchanged)
├── executor.go          # Executor with builtin dispatch (unchanged)
├── readtracker.go       # Read-before-write safety (unchanged)
├── agentlog.go          # Agent logging (unchanged)
├── builtins.go          # read, write, bash (unchanged)
├── definitions.go       # InputSchema + ToolDef wiring (shrinks as tools become builtins)
├── filewalk.go          # Shared: directory walking, extension filtering, file reading
├── builtin_tabcount.go  # Phase 1
├── builtin_notab.go     # Phase 1
├── builtin_delete.go    # Phase 1
├── builtin_split.go     # Phase 2
├── builtin_splice.go    # Phase 2
├── builtin_stump.go     # Phase 2
├── builtin_checkfor.go  # Phase 3
├── builtin_conflicts.go # Phase 3
├── builtin_repfor.go    # Phase 4
├── builtin_cleandiff.go # Phase 4 (still calls git)
├── builtin_utf8.go      # Phase 5
├── builtin_imports.go   # Phase 5
├── builtin_sig.go       # Phase 6a (Go extractor)
├── builtin_sig_ts.go    # Phase 6b (TypeScript extractor)
├── builtin_sig_cs.go    # Phase 6c (C# extractor)
├── builtin_transform.go # Phase 6
├── builtin_errs.go      # Phase 6
└── *_test.go            # Tests per phase
```

## Per-Phase Workflow

For each phase:

1. **Read the original tool source** in `$HOME/Documents/Code/Go_dev/terse-mcp/<tool>/`
2. **Implement the builtin** in `internal/tools/builtin_<tool>.go`:
   - Define the `BuiltinFunc` implementation
   - Define the `ToolDef` with `Builtin` field set, `Binary` empty
   - Match the original tool's JSON output format exactly
3. **Move the definition** from `AllTools()` (subprocess) to `BuiltinTools()` (builtin)
4. **Write tests** that verify output matches the original tool:
   - Unit tests for core logic
   - Integration test: run both the original binary and the builtin on the same input, compare JSON output
5. **Run full test suite**: `go test ./internal/tools/` + `go test ./...`
6. **Update DefaultRegistry count** test (currently expects 18)

## Shared Infrastructure (Phase 0)

Before Phase 1, extract shared utilities into `filewalk.go`:

```go
// filewalk.go — shared file discovery and reading for checkfor, repfor, stump

// WalkOptions controls directory traversal.
type WalkOptions struct {
    Dirs       []string // directories to scan
    Files      []string // specific files (bypass dir scan)
    Ext        string   // extension filter (e.g. ".go")
    Recursive  bool     // recurse into subdirectories
    SkipHidden bool     // skip dotfiles/dotdirs
}

// WalkFiles yields file paths matching the options.
func WalkFiles(opts WalkOptions) ([]string, error)

// ReadLines reads a file and returns lines with their line numbers.
func ReadLines(path string) ([]string, error)

// AtomicWrite writes content to a file via temp+rename for crash safety.
func AtomicWrite(path string, content []byte, perm os.FileMode) error

// StripANSI removes ANSI escape codes from a string.
func StripANSI(s string) string
```

## Verification Strategy

Each builtin must produce **byte-identical JSON output** to the original CLI tool for the same input. Verification approach:

```go
func TestBuiltinCheckfor_MatchesCLI(t *testing.T) {
    // Skip if binary not on PATH
    if _, err := exec.LookPath("checkfor"); err != nil {
        t.Skip("checkfor binary not found")
    }

    input := map[string]any{"search": "func", "dir": []any{"."}, "ext": ".go"}

    // Run builtin
    builtinResult := builtinCheckfor(ctx, input, workDir)

    // Run CLI
    cliResult := runCLI("checkfor", "--cli", "--search", "func", "--dir", ".", "--ext", ".go")

    // Compare JSON (normalize ordering)
    assertJSONEqual(t, builtinResult.Output, cliResult)
}
```

This dual-execution pattern catches output format drift.

## Migration Checklist

After all phases complete:

- [ ] `AllTools()` returns empty slice (or is removed)
- [ ] `BuiltinTools()` returns all 18 tools (15 reimplemented + read/write/bash)
- [ ] `definitions.go` contains only InputSchemas (no Binary/FlagMap)
- [ ] `CheckBinaries()` returns empty for all tools (only `git` is external, used by cleanDiff)
- [ ] `executeSubprocess()` is dead code (can be removed or kept for user-defined tools)
- [ ] `FlagSpec` type and `buildArgs()` are dead code (can be removed)
- [ ] All existing tests pass
- [ ] `nostop` binary works with zero external tool binaries installed (except `git` for cleanDiff)

## Ordering Rationale

Phases are ordered by:
1. **Increasing difficulty** — build confidence with easy tools first
2. **Dependency graph** — `filewalk.go` (Phase 0) is needed by checkfor/repfor/stump. checkfor logic informs repfor.
3. **Usage frequency** — tabcount, notab, stump, checkfor are the most commonly called tools in agentic sessions. Getting them native early has the most impact.
4. **sig last** — 3,500 LOC across 3 language extractors. Most complex, least urgent (the binary works fine as a fallback).
