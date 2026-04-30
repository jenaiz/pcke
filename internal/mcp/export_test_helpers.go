package mcp

// TemplateDef is the exported version of templateDef for testing.
type TemplateDef struct {
	Name        string
	Description string
	HasModule   bool
	Sections    []string
}

// LoadCustomTemplates is the exported wrapper of loadCustomTemplates for testing.
func LoadCustomTemplates(root string) []TemplateDef {
	defs := loadCustomTemplates(root)
	out := make([]TemplateDef, len(defs))
	for i, d := range defs {
		out[i] = TemplateDef{
			Name:        d.name,
			Description: d.description,
			HasModule:   d.hasModule,
			Sections:    d.sections,
		}
	}
	return out
}

// FilterValidSections is the exported wrapper of filterValidSections for testing.
func FilterValidSections(sections []string) []string {
	return filterValidSections(sections)
}
