package tools

// AllTools returns definitions for subprocess-based terse-mcp CLI tools.
// All 15 tools have been migrated to builtins. This function returns an empty slice.
func AllTools() []ToolDef {
	return []ToolDef{}
}

// All tools migrated to builtins:
// CheckforDef → builtin_checkfor.go
// RepforDef → builtin_repfor.go
// SigDef → builtin_sig.go + builtin_sig_ts.go + builtin_sig_cs.go
// CleanDiffDef → builtin_cleandiff.go
// ErrsDef → builtin_errs.go
// TransformDef → builtin_transform.go
// ImportsDef → builtin_imports.go
// StumpDef → builtin_stump.go
// ConflictsDef → builtin_conflicts.go
// NotabDef → builtin_notab.go
// DeleteDef → builtin_delete.go
// UTF8Def → builtin_utf8.go
// TabcountDef → builtin_tabcount.go
// SplitDef → builtin_split.go
// SpliceDef → builtin_splice.go
