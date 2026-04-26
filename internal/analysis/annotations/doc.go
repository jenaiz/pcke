// Package annotations extracts @pcke-rule and @pcke-lesson annotations
// from source code comments across supported languages (Go, Python,
// JavaScript, TypeScript, Java).
//
// Annotations are embedded directly in source comments and indexed as
// first-class knowledge nodes during `pcke scan --deep`. The supported
// syntax is:
//
//	@pcke-rule <name>: <description>
//	@pcke-lesson <name>: <description>
//
// Phase 3 — Task F3.T7.
package annotations
