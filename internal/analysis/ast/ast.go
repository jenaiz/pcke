//go:build cgo

// Package ast provides tree-sitter powered AST analysis for source code
// entity extraction. It supports Go, Python, JavaScript, TypeScript, and Java.
//
// This is the F2.T3 deliverable: CGo integration + language bindings.
// Downstream consumers (F2.T4 scan --deep) call [Parser.ParseFile] to
// obtain entities and imports from a single source file.
package ast

import (
	"context"
	"fmt"
	"os"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// EntityKind classifies an extracted code entity.
type EntityKind string

// Supported entity kinds.
const (
	KindFunction   EntityKind = "function"
	KindMethod     EntityKind = "method"
	KindStruct     EntityKind = "struct"
	KindInterface  EntityKind = "interface"
	KindClass      EntityKind = "class"
	KindConstant   EntityKind = "constant"
	KindEnum       EntityKind = "enum"
	KindAnnotation EntityKind = "annotation"
)

// Entity represents a single code entity extracted from AST analysis.
type Entity struct {
	Kind     EntityKind `json:"kind"`
	Name     string     `json:"name"`
	StartRow uint32     `json:"start_row"`
	EndRow   uint32     `json:"end_row"`
	Exported bool       `json:"exported"`
	Receiver string     `json:"receiver,omitempty"` // Go methods only.
	Doc      string     `json:"doc,omitempty"`      // Leading doc comment if any.
}

// Import represents an extracted import/dependency.
type Import struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
}

// ParseResult holds all entities and imports extracted from a single file.
type ParseResult struct {
	Language string   `json:"language"`
	Entities []Entity `json:"entities"`
	Imports  []Import `json:"imports"`
}

// Lang identifies a supported programming language.
type Lang int

// Supported languages for AST parsing.
const (
	LangUnknown    Lang = iota
	LangGo              // Go
	LangPython          // Python
	LangJavaScript      // JavaScript
	LangTypeScript      // TypeScript
	LangJava            // Java
)

// langFromExt maps file extensions to Lang.
func langFromExt(ext string) Lang {
	switch ext {
	case ".go":
		return LangGo
	case ".py":
		return LangPython
	case ".js", ".jsx", ".mjs", ".cjs":
		return LangJavaScript
	case ".ts", ".tsx":
		return LangTypeScript
	case ".java":
		return LangJava
	default:
		return LangUnknown
	}
}

// langName returns the human-readable language name.
func langName(l Lang) string {
	switch l {
	case LangGo:
		return "Go"
	case LangPython:
		return "Python"
	case LangJavaScript:
		return "JavaScript"
	case LangTypeScript:
		return "TypeScript"
	case LangJava:
		return "Java"
	default:
		return "unknown"
	}
}

// tsLang returns the tree-sitter Language for a Lang.
func tsLang(l Lang) *sitter.Language {
	switch l {
	case LangGo:
		return golang.GetLanguage()
	case LangPython:
		return python.GetLanguage()
	case LangJavaScript:
		return javascript.GetLanguage()
	case LangTypeScript:
		return typescript.GetLanguage()
	case LangJava:
		return java.GetLanguage()
	default:
		return nil
	}
}

// Parser wraps tree-sitter for multi-language AST analysis.
type Parser struct {
	parser *sitter.Parser
}

// NewParser creates a new AST parser.
func NewParser() *Parser {
	return &Parser{parser: sitter.NewParser()}
}

// Close releases tree-sitter resources.
func (p *Parser) Close() {
	p.parser.Close()
}

// ParseFile parses a source file and extracts entities + imports.
// ext is the file extension (e.g. ".go"). Returns nil result for
// unsupported languages.
func (p *Parser) ParseFile(ctx context.Context, path, ext string) (*ParseResult, error) {
	lang := langFromExt(ext)
	if lang == LangUnknown {
		return nil, nil
	}

	src, err := os.ReadFile(path) //nolint:gosec // G304: path provided by scanner from known root.
	if err != nil {
		return nil, fmt.Errorf("ast: read %s: %w", path, err)
	}

	return p.ParseBytes(ctx, src, lang)
}

// ParseBytes parses source code bytes and extracts entities + imports.
func (p *Parser) ParseBytes(ctx context.Context, src []byte, lang Lang) (*ParseResult, error) {
	tsLanguage := tsLang(lang)
	if tsLanguage == nil {
		return nil, nil
	}

	p.parser.SetLanguage(tsLanguage)

	tree, err := p.parser.ParseCtx(ctx, nil, src)
	if err != nil {
		return nil, fmt.Errorf("ast: parse: %w", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	result := &ParseResult{Language: langName(lang)}

	switch lang {
	case LangGo:
		result.Entities = extractGoEntities(root, src)
		result.Imports = extractGoImports(root, src)
	case LangPython:
		result.Entities = extractPythonEntities(root, src)
		result.Imports = extractPythonImports(root, src)
	case LangJavaScript, LangTypeScript:
		result.Entities = extractJSEntities(root, src)
		result.Imports = extractJSImports(root, src)
	case LangJava:
		result.Entities = extractJavaEntities(root, src)
		result.Imports = extractJavaImports(root, src)
	}

	return result, nil
}

// Supported returns the list of supported file extensions.
func Supported() []string {
	return []string{".go", ".py", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".java"}
}

// IsSupported returns true if the file extension is supported for AST analysis.
func IsSupported(ext string) bool {
	return langFromExt(ext) != LangUnknown
}
