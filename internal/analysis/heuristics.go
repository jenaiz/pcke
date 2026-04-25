// Package analysis implements the Phase 0 analysis engine for pcke.
//
// It provides file tree scanning, git intelligence via go-git, secrets
// filtering, and path-based classification heuristics. The scanner
// orchestrates these components and persists results to the kdb store
// via [kdb.DB.Update] transactions.
//
// Concurrency: a single Scanner instance should be used per scan
// invocation. The underlying [kdb.DB] manages its own locking.
//
// See PRD §5.5 for the analysis pipeline design.
package analysis

import (
	"path/filepath"
	"strings"
)

// FileClass categorises a file by its role in the project.
type FileClass int

const (
	// ClassUnknown indicates the file role could not be determined.
	ClassUnknown FileClass = iota
	// ClassSource marks production source code.
	ClassSource
	// ClassTest marks test files.
	ClassTest
	// ClassEntryPoint marks CLI or application entry points.
	ClassEntryPoint
	// ClassAPI marks API/route definition files.
	ClassAPI
	// ClassDataLayer marks model/entity files.
	ClassDataLayer
	// ClassInfra marks infrastructure-as-code files.
	ClassInfra
	// ClassConfig marks configuration files.
	ClassConfig
	// ClassDoc marks documentation files.
	ClassDoc
	// ClassAsset marks non-code assets (images, fonts, etc.).
	ClassAsset
)

// String returns a human-readable label for the classification.
func (c FileClass) String() string {
	switch c {
	case ClassSource:
		return "source"
	case ClassTest:
		return "test"
	case ClassEntryPoint:
		return "entry_point"
	case ClassAPI:
		return "api"
	case ClassDataLayer:
		return "data_layer"
	case ClassInfra:
		return "infra"
	case ClassConfig:
		return "config"
	case ClassDoc:
		return "doc"
	case ClassAsset:
		return "asset"
	default:
		return "unknown"
	}
}

// Classify determines the [FileClass] for a file based on its path.
// The relPath should be slash-separated and relative to the repository root.
func Classify(relPath string) FileClass {
	relPath = filepath.ToSlash(relPath)
	base := filepath.Base(relPath)
	ext := strings.ToLower(filepath.Ext(base))
	lower := strings.ToLower(relPath)

	// Test files — check before source to avoid misclassification.
	if isTestFile(base, ext) {
		return ClassTest
	}

	// Infrastructure files.
	if isInfraFile(base, ext, lower) {
		return ClassInfra
	}

	// Config files.
	if isConfigFile(base, ext) {
		return ClassConfig
	}

	// Documentation.
	if isDocFile(base, ext) {
		return ClassDoc
	}

	// Assets.
	if isAssetFile(ext) {
		return ClassAsset
	}

	// Path-based classifications (entry point, API, data layer).
	if cls := classifyByPath(lower); cls != ClassUnknown {
		return cls
	}

	// Remaining source files.
	if isSourceExt(ext) {
		return ClassSource
	}

	return ClassUnknown
}

func isTestFile(base, ext string) bool {
	switch {
	case ext == ".go" && strings.HasSuffix(base, "_test.go"):
		return true
	case (ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx") &&
		(strings.Contains(base, ".spec.") || strings.Contains(base, ".test.")):
		return true
	case ext == ".py" && (strings.HasPrefix(base, "test_") || strings.HasSuffix(strings.TrimSuffix(base, ext), "_test")):
		return true
	case strings.Contains(strings.ToLower(base), "test") && ext == ".java":
		return true
	}
	return false
}

func isInfraFile(base, ext, lower string) bool {
	switch {
	case base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile."):
		return true
	case ext == ".tf" || ext == ".hcl":
		return true
	case base == "docker-compose.yml" || base == "docker-compose.yaml":
		return true
	case strings.Contains(lower, "kubernetes/") || strings.Contains(lower, "k8s/"):
		return true
	case strings.Contains(lower, "helm/"):
		return true
	}
	return false
}

func isConfigFile(base, ext string) bool {
	switch base {
	case "Makefile", "Rakefile", "Taskfile.yml", "Justfile",
		".golangci.yml", ".eslintrc.json", ".prettierrc",
		"tsconfig.json", "package.json", "go.mod", "go.sum",
		"Cargo.toml", "Cargo.lock", "pyproject.toml", "setup.py",
		"Gemfile", "Gemfile.lock", "requirements.txt", "pom.xml",
		".gitignore", ".dockerignore", ".editorconfig":
		return true
	}
	switch ext {
	case ".toml", ".ini", ".cfg":
		return true
	}
	return false
}

func isDocFile(base, ext string) bool {
	switch ext {
	case ".md", ".rst", ".adoc", ".txt":
		return true
	}
	switch base {
	case "LICENSE", "NOTICE", "AUTHORS", "CHANGELOG", "CONTRIBUTING":
		return true
	}
	return false
}

func isAssetFile(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
		".woff", ".woff2", ".ttf", ".eot",
		".mp3", ".mp4", ".wav", ".ogg",
		".pdf", ".zip", ".tar", ".gz":
		return true
	}
	return false
}

func classifyByPath(lower string) FileClass {
	parts := strings.Split(lower, "/")
	for _, p := range parts {
		switch p {
		case "cmd", "cli", "bin":
			return ClassEntryPoint
		case "api", "routes", "handlers", "endpoints", "controllers":
			return ClassAPI
		case "models", "entities", "schema", "domain":
			return ClassDataLayer
		}
	}
	return ClassUnknown
}

// extToLanguage maps file extensions to programming language names.
var extToLanguage = map[string]string{
	".go": "Go", ".py": "Python", ".js": "JavaScript", ".ts": "TypeScript",
	".tsx": "TypeScript", ".jsx": "JavaScript", ".rs": "Rust", ".java": "Java",
	".rb": "Ruby", ".c": "C", ".cpp": "C++", ".cc": "C++", ".cxx": "C++",
	".h": "C/C++ Header", ".hpp": "C/C++ Header", ".cs": "C#", ".swift": "Swift",
	".kt": "Kotlin", ".kts": "Kotlin", ".scala": "Scala",
	".sh": "Shell", ".bash": "Shell", ".zsh": "Shell",
	".sql": "SQL", ".html": "HTML", ".htm": "HTML",
	".css": "CSS", ".scss": "CSS", ".less": "CSS",
	".yaml": "YAML", ".yml": "YAML", ".json": "JSON", ".xml": "XML",
	".proto": "Protobuf", ".tf": "Terraform", ".md": "Markdown", ".toml": "TOML",
	".lua": "Lua", ".r": "R", ".zig": "Zig",
	".ex": "Elixir", ".exs": "Elixir", ".erl": "Erlang",
	".hs": "Haskell", ".ml": "OCaml", ".mli": "OCaml",
	".pl": "Perl", ".pm": "Perl", ".php": "PHP", ".dart": "Dart",
}

// Language returns the detected programming language from a file extension.
// Returns an empty string for unrecognised extensions.
func Language(ext string) string {
	if lang, ok := extToLanguage[strings.ToLower(ext)]; ok {
		return lang
	}
	return ""
}

// DetectModule infers a module name from a file path. It returns the
// first directory component under well-known roots (internal/, pkg/,
// lib/, src/, cmd/) or the first directory component if no known root
// is found. For files at the repository root, it returns "(root)".
func DetectModule(relPath string) string {
	relPath = filepath.ToSlash(relPath)
	parts := strings.Split(relPath, "/")

	if len(parts) <= 1 {
		return "(root)"
	}

	// Look for well-known root directories.
	roots := []string{"internal", "pkg", "lib", "src", "cmd", "app", "packages"}
	for i, p := range parts {
		for _, root := range roots {
			if p == root && i+1 < len(parts)-1 {
				return root + "/" + parts[i+1]
			}
		}
	}

	// Fallback: first directory component.
	return parts[0]
}

// isSourceExt returns true if the extension belongs to a source code file.
func isSourceExt(ext string) bool {
	return Language(ext) != "" && ext != ".md" && ext != ".json" && ext != ".yaml" && ext != ".yml" && ext != ".xml" && ext != ".toml"
}
