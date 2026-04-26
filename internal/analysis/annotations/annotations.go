package annotations

import (
	"bufio"
	"io"
	"strings"
)

// Type distinguishes between the two supported annotation kinds.
type Type string

// Annotation type constants.
const (
	Rule   Type = "rule"
	Lesson Type = "lesson"
)

// Annotation represents a single extracted @pcke-rule or @pcke-lesson
// from a source file comment.
type Annotation struct {
	Type        Type   // "rule" or "lesson"
	Name        string // identifier after the tag (e.g., "no-raw-sql")
	Description string // text after the colon
	File        string // source file path
	Line        int    // 1-based line number where the annotation was found
}

const (
	prefixRule   = "@pcke-rule"
	prefixLesson = "@pcke-lesson"
)

// Extract scans a reader for @pcke-rule and @pcke-lesson annotations in
// source code comments. It handles single-line comment styles for all
// supported languages:
//   - Go, Java, JavaScript, TypeScript: // ...
//   - Python: # ...
//
// The file parameter is used to populate Annotation.File for context.
func Extract(r io.Reader, file string) []Annotation {
	var results []Annotation
	scanner := bufio.NewScanner(r)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		comment := extractComment(line)
		if comment == "" {
			continue
		}

		if ann, ok := parseAnnotation(comment, file, lineNum); ok {
			results = append(results, ann)
		}
	}

	return results
}

// ExtractFromString is a convenience wrapper around Extract that reads from a string.
func ExtractFromString(source, file string) []Annotation {
	return Extract(strings.NewReader(source), file)
}

// extractComment strips comment markers from a line and returns the inner text.
// Returns empty string if the line is not a comment.
func extractComment(line string) string {
	trimmed := strings.TrimSpace(line)

	// // comment (Go, Java, JS, TS)
	if strings.HasPrefix(trimmed, "//") {
		return strings.TrimSpace(trimmed[2:])
	}

	// # comment (Python)
	if strings.HasPrefix(trimmed, "#") {
		return strings.TrimSpace(trimmed[1:])
	}

	// * inside a block comment (Java, JS, TS)
	if strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "*/") {
		return strings.TrimSpace(trimmed[1:])
	}

	return ""
}

// parseAnnotation checks if a comment contains a @pcke-rule or @pcke-lesson
// annotation and extracts its components.
func parseAnnotation(comment, file string, line int) (Annotation, bool) {
	var ann Annotation

	switch {
	case strings.HasPrefix(comment, prefixRule):
		ann.Type = Rule
		rest := strings.TrimSpace(comment[len(prefixRule):])
		ann.Name, ann.Description = splitNameDesc(rest)
	case strings.HasPrefix(comment, prefixLesson):
		ann.Type = Lesson
		rest := strings.TrimSpace(comment[len(prefixLesson):])
		ann.Name, ann.Description = splitNameDesc(rest)
	default:
		return ann, false
	}

	ann.File = file
	ann.Line = line

	return ann, ann.Name != ""
}

// splitNameDesc splits "name: description" into its components.
// The name is the part before the first colon; description is the rest.
func splitNameDesc(s string) (name, desc string) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		// No colon: treat the whole string as the name (no description).
		name = strings.TrimSpace(s)
		return name, ""
	}

	name = strings.TrimSpace(s[:idx])
	desc = strings.TrimSpace(s[idx+1:])
	return name, desc
}
