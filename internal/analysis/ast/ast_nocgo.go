//go:build !cgo

// Package ast provides tree-sitter powered AST analysis for source code
// entity extraction. This file provides no-op stubs for builds without CGO
// (e.g. release binaries built with CGO_ENABLED=0).
package ast

import "context"

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
	Receiver string     `json:"receiver,omitempty"`
	Doc      string     `json:"doc,omitempty"`
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

// Parser wraps tree-sitter for multi-language AST analysis.
// When built without CGO, all parse methods return nil.
type Parser struct{}

// NewParser creates a new AST parser (no-op without CGO).
func NewParser() *Parser { return &Parser{} }

// Close releases tree-sitter resources (no-op without CGO).
func (p *Parser) Close() {}

// ParseFile parses a source file and extracts entities + imports.
// Without CGO, always returns nil.
func (p *Parser) ParseFile(_ context.Context, _, _ string) (*ParseResult, error) {
	return nil, nil
}

// ParseBytes parses source code bytes and extracts entities + imports.
// Without CGO, always returns nil.
func (p *Parser) ParseBytes(_ context.Context, _ []byte, _ Lang) (*ParseResult, error) {
	return nil, nil
}

// Supported returns the list of supported file extensions.
func Supported() []string {
	return []string{".go", ".py", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".java"}
}

// IsSupported returns true if the file extension is supported for AST analysis.
func IsSupported(ext string) bool {
	switch ext {
	case ".go", ".py", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".java":
		return true
	default:
		return false
	}
}
