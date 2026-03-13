package tools

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"time"
)

// FileShape is the top-level output.
type FileShape struct {
	File      string     `json:"file"`
	Package   string     `json:"package"`
	Imports   []string   `json:"imports,omitempty"`
	Types     []TypeDef  `json:"types,omitempty"`
	Functions []FuncDef  `json:"functions,omitempty"`
	Constants []ValueDef `json:"constants,omitempty"`
	Variables []ValueDef `json:"variables,omitempty"`
}

// TypeDef describes a type declaration.
type TypeDef struct {
	Name       string     `json:"name"`
	Kind       string     `json:"kind"` // struct, interface, named, alias
	Line       int        `json:"line"`
	Fields     []FieldDef `json:"fields,omitempty"`
	Methods    []FuncDef  `json:"methods,omitempty"`
	Embeds     []string   `json:"embeds,omitempty"`
	Underlying string     `json:"underlying,omitempty"`
}

// FieldDef describes a struct field.
type FieldDef struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

// FuncDef describes a function or method.
type FuncDef struct {
	Name      string `json:"name"`
	Receiver  string `json:"receiver,omitempty"`
	Signature string `json:"signature"`
	Line      int    `json:"line"`
}

// ValueDef describes a const or var.
type ValueDef struct {
	Name  string `json:"name"`
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
	Line  int    `json:"line"`
}

// Supported extensions for native extraction.
var sigNativeExts = map[string]bool{
	".go": true,
}

// SigDef extracts the public API surface from source files.
var SigDef = ToolDef{
	Name:        "sig",
	Description: "Extract the public API surface from a source file. Returns function signatures, type/struct/interface definitions, const/var blocks as compact JSON without implementation bodies. Supports Go, TypeScript, and C#.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Absolute path to the source file to analyze.",
			},
			"all": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Include unexported/private symbols.",
			},
		},
		"required": []string{"file"},
	},
	Builtin: builtinSig,
	Timeout: 15 * time.Second,
}

func builtinSig(ctx context.Context, input map[string]any, workDir string) Result {
	filePath, ok := input["file"].(string)
	if !ok || filePath == "" {
		return Result{IsError: true, Error: "file is required"}
	}
	filePath = resolvePath(filePath, workDir)

	exportedOnly := true
	if v, ok := input["all"].(bool); ok && v {
		exportedOnly = false
	}

	ext := filepath.Ext(filePath)

	// Native Go extraction
	if sigNativeExts[ext] {
		shape, err := sigExtractGo(filePath, exportedOnly)
		if err != nil {
			return Result{IsError: true, Error: err.Error()}
		}
		return resultJSON(shape)
	}

	// Native TS extraction
	if ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts" {
		tsExt := &TSExtractor{}
		shape, err := tsExt.Extract(filePath, exportedOnly)
		if err != nil {
			return Result{IsError: true, Error: err.Error()}
		}
		return resultJSON(shape)
	}

	// Native C# extraction
	if ext == ".cs" {
		csExt := &CSExtractor{}
		shape, err := csExt.Extract(filePath, exportedOnly)
		if err != nil {
			return Result{IsError: true, Error: err.Error()}
		}
		return resultJSON(shape)
	}

	supported := []string{".go", ".ts", ".tsx", ".mts", ".cts", ".cs"}
	return Result{IsError: true, Error: fmt.Sprintf("unsupported file type %q (supported: %s)", ext, strings.Join(supported, ", "))}
}

// --- Go extractor (native, uses go/ast) ---

func sigExtractGo(filePath string, exportedOnly bool) (*FileShape, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	shape := &FileShape{
		File:    filePath,
		Package: file.Name.Name,
	}

	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
			path = imp.Name.Name + " " + path
		}
		shape.Imports = append(shape.Imports, path)
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			sigExtractGenDecl(fset, d, shape, exportedOnly)
		case *ast.FuncDecl:
			if exportedOnly && !d.Name.IsExported() {
				continue
			}
			fd := FuncDef{
				Name:      d.Name.Name,
				Signature: sigFuncSignature(fset, d.Type),
				Line:      fset.Position(d.Pos()).Line,
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				fd.Receiver = sigExprString(fset, d.Recv.List[0].Type)
			}
			shape.Functions = append(shape.Functions, fd)
		}
	}

	return shape, nil
}

func sigExtractGenDecl(fset *token.FileSet, d *ast.GenDecl, shape *FileShape, exportedOnly bool) {
	switch d.Tok {
	case token.TYPE:
		for _, spec := range d.Specs {
			ts := spec.(*ast.TypeSpec)
			if exportedOnly && !ts.Name.IsExported() {
				continue
			}
			shape.Types = append(shape.Types, sigExtractType(fset, ts))
		}
	case token.CONST:
		sigExtractValues(fset, d, &shape.Constants, exportedOnly)
	case token.VAR:
		sigExtractValues(fset, d, &shape.Variables, exportedOnly)
	}
}

func sigExtractValues(fset *token.FileSet, d *ast.GenDecl, dest *[]ValueDef, exportedOnly bool) {
	for _, spec := range d.Specs {
		vs := spec.(*ast.ValueSpec)
		for i, name := range vs.Names {
			if exportedOnly && !name.IsExported() {
				continue
			}
			vd := ValueDef{
				Name: name.Name,
				Line: fset.Position(name.Pos()).Line,
			}
			if vs.Type != nil {
				vd.Type = sigExprString(fset, vs.Type)
			}
			if i < len(vs.Values) {
				vd.Value = sigExprString(fset, vs.Values[i])
			}
			*dest = append(*dest, vd)
		}
	}
}

func sigExtractType(fset *token.FileSet, ts *ast.TypeSpec) TypeDef {
	td := TypeDef{
		Name: ts.Name.Name,
		Line: fset.Position(ts.Name.Pos()).Line,
	}

	if ts.TypeParams != nil && len(ts.TypeParams.List) > 0 {
		td.Name += "[" + sigFieldListString(fset, ts.TypeParams.List) + "]"
	}

	switch t := ts.Type.(type) {
	case *ast.StructType:
		td.Kind = "struct"
		if t.Fields != nil {
			for _, f := range t.Fields.List {
				if len(f.Names) == 0 {
					td.Fields = append(td.Fields, FieldDef{
						Type: sigExprString(fset, f.Type),
						Tag:  sigTagString(f.Tag),
					})
				} else {
					for _, name := range f.Names {
						td.Fields = append(td.Fields, FieldDef{
							Name: name.Name,
							Type: sigExprString(fset, f.Type),
							Tag:  sigTagString(f.Tag),
						})
					}
				}
			}
		}
	case *ast.InterfaceType:
		td.Kind = "interface"
		if t.Methods != nil {
			for _, m := range t.Methods.List {
				if len(m.Names) > 0 {
					if ft, ok := m.Type.(*ast.FuncType); ok {
						td.Methods = append(td.Methods, FuncDef{
							Name:      m.Names[0].Name,
							Signature: sigFuncSignature(fset, ft),
							Line:      fset.Position(m.Pos()).Line,
						})
					}
				} else {
					td.Embeds = append(td.Embeds, sigExprString(fset, m.Type))
				}
			}
		}
	default:
		if ts.Assign.IsValid() {
			td.Kind = "alias"
		} else {
			td.Kind = "named"
		}
		td.Underlying = sigExprString(fset, ts.Type)
	}

	return td
}

func sigExprString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, expr); err != nil {
		return "<error>"
	}
	return buf.String()
}

func sigFuncSignature(fset *token.FileSet, ft *ast.FuncType) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, ft); err != nil {
		return "<error>"
	}
	return strings.TrimPrefix(buf.String(), "func")
}

func sigFieldListString(fset *token.FileSet, fields []*ast.Field) string {
	var parts []string
	for _, f := range fields {
		typeStr := sigExprString(fset, f.Type)
		if len(f.Names) > 0 {
			names := make([]string, len(f.Names))
			for i, n := range f.Names {
				names[i] = n.Name
			}
			parts = append(parts, strings.Join(names, ", ")+" "+typeStr)
		} else {
			parts = append(parts, typeStr)
		}
	}
	return strings.Join(parts, ", ")
}

func sigTagString(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	return tag.Value
}
