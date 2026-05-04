package federation

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// CrossRepoDep describes an import dependency from one repo to another.
type CrossRepoDep struct {
	SourceRepo   string
	SourceNodeID string
	TargetRepo   string
	TargetModule string
	ImportPath   string
	DetectedVia  string // "go-import", "python-import", "module-name"
}

// DetectCrossRepoDeps scans the local repo for imports that reference
// packages owned by other federated repos.
func DetectCrossRepoDeps(ctx context.Context, manifest *Manifest, localRepoPath string) ([]CrossRepoDep, error) {
	// Build a map of module paths → repo name from go.mod of each federated repo.
	repoModules := buildRepoModuleMap(manifest, localRepoPath)
	if len(repoModules) == 0 {
		return nil, nil
	}

	// Scan Go source files in local repo for matching imports.
	localName := localRepoName(manifest, localRepoPath)
	var deps []CrossRepoDep
	err := filepath.Walk(localRepoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip inaccessible paths
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Skip hidden dirs and vendor.
		base := filepath.Base(path)
		if info.IsDir() && (strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules") {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		relPath, _ := filepath.Rel(localRepoPath, path)
		deps = append(deps, matchImports(path, relPath, localName, repoModules)...)
		return nil
	})
	if err != nil {
		return deps, fmt.Errorf("detect cross-repo deps: %w", err)
	}
	return deps, nil
}

// buildRepoModuleMap reads go.mod from each federated repo and builds
// a map of Go module path → repo name.
func buildRepoModuleMap(manifest *Manifest, localRepoPath string) map[string]string {
	repoModules := make(map[string]string)
	for _, r := range manifest.Repos {
		if r.Path == localRepoPath {
			continue
		}
		modPath := readGoModulePath(r.Path)
		if modPath != "" {
			repoModules[modPath] = r.Name
		}
	}
	return repoModules
}

// matchImports checks a Go file's imports against known repo module paths.
func matchImports(path, relPath, sourceName string, repoModules map[string]string) []CrossRepoDep {
	imports := extractGoImports(path)
	var deps []CrossRepoDep
	for _, imp := range imports {
		for modPath, repoName := range repoModules {
			if strings.HasPrefix(imp, modPath) || imp == modPath {
				targetMod := strings.TrimPrefix(imp, modPath)
				targetMod = strings.TrimPrefix(targetMod, "/")
				if targetMod == "" {
					targetMod = "(root)"
				}
				deps = append(deps, CrossRepoDep{
					SourceRepo:   sourceName,
					SourceNodeID: relPath,
					TargetRepo:   repoName,
					TargetModule: targetMod,
					ImportPath:   imp,
					DetectedVia:  "go-import",
				})
			}
		}
	}
	return deps
}

// StoreCrossRepoDeps writes cross-repo dependencies into the local DB
// under the "fr:" (federation_relations) prefix.
func StoreCrossRepoDeps(ctx context.Context, db *kdb.DB, deps []CrossRepoDep) error {
	return db.Update(ctx, func(wtx *tx.WriteTx) error {
		// Clear existing federation relations.
		prefix := []byte("fr:")
		cursor := wtx.Cursor()
		var toDelete [][]byte
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "fr:") {
				break
			}
			toDelete = append(toDelete, append([]byte(nil), k...))
		}
		for _, k := range toDelete {
			if err := wtx.Delete(k); err != nil {
				return err
			}
		}

		// Write new deps.
		for i, dep := range deps {
			key := fmt.Sprintf("fr:%s\u2192%s#%d", dep.SourceRepo, dep.TargetRepo, i)
			val := fmt.Sprintf(`{"source_repo":"%s","source_node_id":"%s","target_repo":"%s","target_module":"%s","import_path":"%s","detected_via":"%s"}`,
				dep.SourceRepo, dep.SourceNodeID, dep.TargetRepo, dep.TargetModule, dep.ImportPath, dep.DetectedVia)
			if err := wtx.Put([]byte(key), []byte(val)); err != nil {
				return err
			}
		}
		return nil
	})
}

// readGoModulePath reads the module path from go.mod in the given directory.
func readGoModulePath(repoPath string) string {
	f, err := os.Open(filepath.Join(repoPath, "go.mod")) //nolint:gosec // G304: path from federated config.
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck // best-effort read
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// extractGoImports parses a Go file and returns all import paths.
func extractGoImports(path string) []string {
	f, err := os.Open(path) //nolint:gosec // G304: paths from workspace walk.
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // best-effort read

	var imports []string
	scanner := bufio.NewScanner(f)
	inBlock := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "import (" {
			inBlock = true
			continue
		}
		if inBlock {
			if line == ")" {
				inBlock = false
				continue
			}
			imp := extractImportPath(line)
			if imp != "" {
				imports = append(imports, imp)
			}
		} else if strings.HasPrefix(line, "import ") {
			imp := extractImportPath(strings.TrimPrefix(line, "import "))
			if imp != "" {
				imports = append(imports, imp)
			}
		}
	}
	return imports
}

// extractImportPath extracts the import path from a line like `"github.com/foo/bar"`.
func extractImportPath(line string) string {
	// Handle named imports: `foo "path"` or just `"path"`.
	idx := strings.Index(line, `"`)
	if idx < 0 {
		return ""
	}
	rest := line[idx+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// localRepoName finds the name of the local repo in the manifest.
func localRepoName(m *Manifest, path string) string {
	for _, r := range m.Repos {
		if r.Path == path {
			return r.Name
		}
	}
	return filepath.Base(path)
}
