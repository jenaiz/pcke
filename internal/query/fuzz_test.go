package query

import "testing"

// FuzzTokenize feeds arbitrary strings to the lexer to find panics.
func FuzzTokenize(f *testing.F) {
	// Seed corpus from PRD examples.
	f.Add("nodes where type = 'module' and stability > 0.7")
	f.Add("nodes where module = 'api' order by updated_at desc limit 10")
	f.Add("constraints where scope = 'global' and severity = 'must'")
	f.Add("evolution where author = 'jesus' and change_type = 'refactored'")
	f.Add("notes where tags contains 'decision'")
	f.Add("")
	f.Add("   ")
	f.Add("'unterminated")
	f.Add("!!!===>>><<<")
	f.Add("a.b.c.d.e = 'x'")

	f.Fuzz(func(_ *testing.T, input string) {
		// Must not panic. Errors are acceptable.
		_, _ = Tokenize(input)
	})
}

// FuzzParse feeds arbitrary strings to the full parse pipeline to find panics.
func FuzzParse(f *testing.F) {
	f.Add("nodes where type = 'module' and stability > 0.7")
	f.Add("nodes where module = 'api' order by updated_at desc limit 10")
	f.Add("constraints where scope = 'global' and severity = 'must'")
	f.Add("evolution where author = 'jesus' and change_type = 'refactored'")
	f.Add("notes where tags contains 'decision'")
	f.Add("")
	f.Add("foobar")
	f.Add("nodes where")
	f.Add("nodes where x = ")
	f.Add("nodes order by")
	f.Add("nodes limit abc")
	f.Add("nodes limit -1")

	f.Fuzz(func(_ *testing.T, input string) {
		// Must not panic. Errors are acceptable.
		q, err := Parse(input)
		if err != nil {
			return
		}
		// If parse succeeds, type check and plan must not panic either.
		_ = TypeCheck(q)
		_ = BuildPlan(q)
	})
}
