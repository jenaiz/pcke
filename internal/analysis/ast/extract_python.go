package ast

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractPythonEntities walks the Python AST and extracts functions,
// classes, and module-level assignments.
func extractPythonEntities(root *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "function_definition":
			if e := pyFunction(child, src); e != nil {
				entities = append(entities, *e)
			}
		case "class_definition":
			entities = append(entities, pyClass(child, src)...)
		case "decorated_definition":
			entities = append(entities, pyDecorated(child, src)...)
		}
	}
	return entities
}

func pyFunction(n *sitter.Node, src []byte) *Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	return &Entity{
		Kind:     KindFunction,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: !strings.HasPrefix(name, "_"),
	}
}

func pyClass(n *sitter.Node, src []byte) []Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	entities := []Entity{{
		Kind:     KindClass,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: !strings.HasPrefix(name, "_"),
	}}

	// Extract methods from class body.
	body := n.ChildByFieldName("body")
	if body != nil {
		entities = append(entities, pyClassMethods(body, src, name)...)
	}
	return entities
}

func pyClassMethods(body *sitter.Node, src []byte, className string) []Entity {
	var methods []Entity
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		switch child.Type() {
		case "function_definition":
			if e := pyMethod(child, src, className); e != nil {
				methods = append(methods, *e)
			}
		case "decorated_definition":
			// Handle @staticmethod, @classmethod, etc.
			for j := 0; j < int(child.NamedChildCount()); j++ {
				inner := child.NamedChild(j)
				if inner.Type() == "function_definition" {
					if e := pyMethod(inner, src, className); e != nil {
						methods = append(methods, *e)
					}
				}
			}
		}
	}
	return methods
}

func pyMethod(n *sitter.Node, src []byte, className string) *Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	return &Entity{
		Kind:     KindMethod,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: !strings.HasPrefix(name, "_"),
		Receiver: className,
	}
}

func pyDecorated(n *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		switch child.Type() {
		case "function_definition":
			if e := pyFunction(child, src); e != nil {
				entities = append(entities, *e)
			}
		case "class_definition":
			entities = append(entities, pyClass(child, src)...)
		}
	}
	return entities
}

// extractPythonImports extracts import statements from Python source.
func extractPythonImports(root *sitter.Node, src []byte) []Import {
	var imports []Import
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "import_statement":
			imports = append(imports, pyImportStmt(child, src)...)
		case "import_from_statement":
			imports = append(imports, pyFromImport(child, src)...)
		}
	}
	return imports
}

func pyImportStmt(n *sitter.Node, src []byte) []Import {
	var imports []Import
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		switch child.Type() {
		case "dotted_name":
			imports = append(imports, Import{Path: child.Content(src)})
		case "aliased_import":
			nameNode := child.ChildByFieldName("name")
			aliasNode := child.ChildByFieldName("alias")
			if nameNode != nil {
				imp := Import{Path: nameNode.Content(src)}
				if aliasNode != nil {
					imp.Alias = aliasNode.Content(src)
				}
				imports = append(imports, imp)
			}
		}
	}
	return imports
}

func pyFromImport(n *sitter.Node, src []byte) []Import {
	var moduleName string
	nameNode := n.ChildByFieldName("module_name")
	if nameNode != nil {
		moduleName = nameNode.Content(src)
	}
	if moduleName == "" {
		return nil
	}
	return []Import{{Path: moduleName}}
}
