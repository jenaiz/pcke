package decisions

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jenaiz/pcke/internal/analysis/annotations"
)

// annotationExtensions are the source-file extensions WalkForAnnotations
// inspects. The annotations package handles the underlying comment
// styles (//, #, * inside block comments).
var annotationExtensions = map[string]bool{
	".go":   true,
	".py":   true,
	".js":   true,
	".ts":   true,
	".tsx":  true,
	".jsx":  true,
	".java": true,
}

// WalkForAnnotations recursively scans a repository root for source
// files and returns every @pcke-rule / @pcke-lesson annotation found.
//
// Skipped entries:
//
//   - hidden directories (names starting with '.') so .git, .pcke,
//     .github, etc. don't pollute the result.
//   - the conventional dependency cache directories: vendor/, node_modules/.
//   - any file whose extension is not in annotationExtensions.
//
// Files that fail to open (permissions, transient I/O) are skipped
// silently — annotation backfill is best-effort and one bad file
// should not abort an entire scan.
func WalkForAnnotations(repoRoot string) ([]annotations.Annotation, error) {
	var out []annotations.Annotation
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == repoRoot {
				return nil
			}
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !annotationExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		f, openErr := os.Open(path) //nolint:gosec // G304: path comes from filepath.WalkDir under the supplied root.
		if openErr != nil {
			return nil // best-effort: keep walking
		}
		defer func() { _ = f.Close() }()

		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			rel = path
		}
		anns := annotations.Extract(f, filepath.ToSlash(rel))
		out = append(out, anns...)
		return nil
	})
	if err != nil {
		return out, fmt.Errorf("walk for annotations: %w", err)
	}
	return out, nil
}

// shouldSkipDir is true for hidden directories and well-known
// dependency caches.
func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "node_modules", "target", "build", "dist":
		return true
	}
	return false
}
