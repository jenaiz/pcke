package ast

import (
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractGoEntities walks the Go AST and extracts functions, methods,
// structs, interfaces, and top-level constants.
func extractGoEntities(root *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "function_declaration":
			if e := goFunction(child, src); e != nil {
				entities = append(entities, *e)
			}
		case "method_declaration":
			if e := goMethod(child, src); e != nil {
				entities = append(entities, *e)
			}
		case "type_declaration":
			entities = append(entities, goTypeDecl(child, src)...)
		case "const_declaration":
			entities = append(entities, goConstDecl(child, src)...)
		}
	}
	return entities
}

func goFunction(n *sitter.Node, src []byte) *Entity {
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
		Exported: isExported(name),
		Doc:      leadingComment(n, src),
	}
}

func goMethod(n *sitter.Node, src []byte) *Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)

	var receiver string
	params := n.ChildByFieldName("receiver")
	if params != nil {
		receiver = extractGoReceiver(params, src)
	}

	return &Entity{
		Kind:     KindMethod,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: isExported(name),
		Receiver: receiver,
		Doc:      leadingComment(n, src),
	}
}

// extractGoReceiver extracts the receiver type name from a parameter_list node.
func extractGoReceiver(params *sitter.Node, src []byte) string {
	for i := 0; i < int(params.NamedChildCount()); i++ {
		param := params.NamedChild(i)
		typeNode := param.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		text := typeNode.Content(src)
		// Strip pointer prefix.
		text = strings.TrimPrefix(text, "*")
		return text
	}
	return ""
}

func goTypeDecl(n *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		if spec.Type() != "type_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)

		typeNode := spec.ChildByFieldName("type")
		kind := goTypeKind(typeNode)

		entities = append(entities, Entity{
			Kind:     kind,
			Name:     name,
			StartRow: spec.StartPoint().Row,
			EndRow:   spec.EndPoint().Row,
			Exported: isExported(name),
			Doc:      leadingComment(n, src),
		})
	}
	return entities
}

func goTypeKind(typeNode *sitter.Node) EntityKind {
	if typeNode == nil {
		return KindStruct
	}
	switch typeNode.Type() {
	case "struct_type":
		return KindStruct
	case "interface_type":
		return KindInterface
	default:
		return KindStruct
	}
}

func goConstDecl(n *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		if spec.Type() != "const_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		entities = append(entities, Entity{
			Kind:     KindConstant,
			Name:     name,
			StartRow: spec.StartPoint().Row,
			EndRow:   spec.EndPoint().Row,
			Exported: isExported(name),
		})
	}
	return entities
}

// extractGoImports extracts import paths from Go source.
func extractGoImports(root *sitter.Node, src []byte) []Import {
	var imports []Import
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child.Type() != "import_declaration" {
			continue
		}
		imports = append(imports, goImportDecl(child, src)...)
	}
	return imports
}

func goImportDecl(n *sitter.Node, src []byte) []Import {
	var imports []Import
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		switch child.Type() {
		case "import_spec":
			if imp := goImportSpec(child, src); imp != nil {
				imports = append(imports, *imp)
			}
		case "import_spec_list":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				spec := child.NamedChild(j)
				if spec.Type() == "import_spec" {
					if imp := goImportSpec(spec, src); imp != nil {
						imports = append(imports, *imp)
					}
				}
			}
		}
	}
	return imports
}

func goImportSpec(n *sitter.Node, src []byte) *Import {
	pathNode := n.ChildByFieldName("path")
	if pathNode == nil {
		return nil
	}
	path := strings.Trim(pathNode.Content(src), `"`)

	var alias string
	nameNode := n.ChildByFieldName("name")
	if nameNode != nil {
		alias = nameNode.Content(src)
	}

	return &Import{Path: path, Alias: alias}
}

// isExported returns true if a Go identifier starts with an uppercase letter.
func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// leadingComment extracts the doc comment directly above a node.
func leadingComment(n *sitter.Node, src []byte) string {
	prev := n.PrevNamedSibling()
	if prev == nil || prev.Type() != "comment" {
		return ""
	}
	// Only use the comment if it immediately precedes the declaration.
	if n.StartPoint().Row-prev.EndPoint().Row > 1 {
		return ""
	}
	text := prev.Content(src)
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimSpace(text)
	return text
}
