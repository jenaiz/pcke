package annotations

import "testing"

func TestExtractFromString(t *testing.T) {
	tests := []struct {
		name   string
		source string
		file   string
		want   []Annotation
	}{
		{
			name:   "Go rule",
			source: `// @pcke-rule no-raw-sql: Always use the query builder, never raw SQL strings.`,
			file:   "main.go",
			want: []Annotation{
				{Type: Rule, Name: "no-raw-sql", Description: "Always use the query builder, never raw SQL strings.", File: "main.go", Line: 1},
			},
		},
		{
			name:   "Go lesson",
			source: `// @pcke-lesson retry-backoff: Exponential backoff with jitter is required for all external calls.`,
			file:   "retry.go",
			want: []Annotation{
				{Type: Lesson, Name: "retry-backoff", Description: "Exponential backoff with jitter is required for all external calls.", File: "retry.go", Line: 1},
			},
		},
		{
			name:   "Python rule",
			source: `# @pcke-rule no-print: Use logging module instead of print().`,
			file:   "utils.py",
			want: []Annotation{
				{Type: Rule, Name: "no-print", Description: "Use logging module instead of print().", File: "utils.py", Line: 1},
			},
		},
		{
			name: "JavaScript rule",
			source: `// Some comment
// @pcke-rule no-var: Use const/let instead of var.
const x = 1;`,
			file: "app.js",
			want: []Annotation{
				{Type: Rule, Name: "no-var", Description: "Use const/let instead of var.", File: "app.js", Line: 2},
			},
		},
		{
			name: "Java block comment",
			source: `/**
 * @pcke-lesson thread-safety: All shared state must be synchronized.
 */`,
			file: "Main.java",
			want: []Annotation{
				{Type: Lesson, Name: "thread-safety", Description: "All shared state must be synchronized.", File: "Main.java", Line: 2},
			},
		},
		{
			name: "multiple annotations",
			source: `// @pcke-rule no-magic: No magic numbers without named constants.
// Some regular comment
// @pcke-lesson error-wrap: Always wrap errors with context.
// @pcke-rule no-panic: Never panic in library code.`,
			file: "lib.go",
			want: []Annotation{
				{Type: Rule, Name: "no-magic", Description: "No magic numbers without named constants.", File: "lib.go", Line: 1},
				{Type: Lesson, Name: "error-wrap", Description: "Always wrap errors with context.", File: "lib.go", Line: 3},
				{Type: Rule, Name: "no-panic", Description: "Never panic in library code.", File: "lib.go", Line: 4},
			},
		},
		{
			name:   "no annotations",
			source: "// regular comment\nfunc main() {}",
			file:   "main.go",
			want:   nil,
		},
		{
			name:   "rule without description",
			source: "// @pcke-rule standalone-name",
			file:   "test.go",
			want: []Annotation{
				{Type: Rule, Name: "standalone-name", Description: "", File: "test.go", Line: 1},
			},
		},
		{
			name:   "empty after prefix is ignored",
			source: "// @pcke-rule",
			file:   "test.go",
			want:   nil, // no name → skipped
		},
		{
			name:   "TypeScript rule",
			source: "// @pcke-rule no-any: Avoid using 'any' type.",
			file:   "app.ts",
			want: []Annotation{
				{Type: Rule, Name: "no-any", Description: "Avoid using 'any' type.", File: "app.ts", Line: 1},
			},
		},
		{
			name:   "Python lesson with indentation",
			source: `    # @pcke-lesson cache-ttl: Always set a TTL on cache entries.`,
			file:   "cache.py",
			want: []Annotation{
				{Type: Lesson, Name: "cache-ttl", Description: "Always set a TTL on cache entries.", File: "cache.py", Line: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFromString(tt.source, tt.file)

			if len(got) != len(tt.want) {
				t.Fatalf("count: got %d, want %d", len(got), len(tt.want))
			}

			for i := range tt.want {
				w := tt.want[i]
				g := got[i]
				if g.Type != w.Type {
					t.Errorf("[%d] Type: got %q, want %q", i, g.Type, w.Type)
				}
				if g.Name != w.Name {
					t.Errorf("[%d] Name: got %q, want %q", i, g.Name, w.Name)
				}
				if g.Description != w.Description {
					t.Errorf("[%d] Description: got %q, want %q", i, g.Description, w.Description)
				}
				if g.File != w.File {
					t.Errorf("[%d] File: got %q, want %q", i, g.File, w.File)
				}
				if g.Line != w.Line {
					t.Errorf("[%d] Line: got %d, want %d", i, g.Line, w.Line)
				}
			}
		})
	}
}
