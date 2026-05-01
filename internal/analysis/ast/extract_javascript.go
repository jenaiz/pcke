//go:build cgo

package ast

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractJSEntities walks a JS/TS AST and extracts functions, classes, and exports.
func extractJSEntities(root *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		entities = append(entities, jsNode(child, src, false)...)
	}
	return entities
}

func jsNode(n *sitter.Node, src []byte, exported bool) []Entity {
	switch n.Type() {
	case "function_declaration":
		if e := jsFunction(n, src, exported); e != nil {
			return []Entity{*e}
		}
	case "class_declaration":
		return jsClass(n, src, exported)
	case "lexical_declaration":
		return jsLexicalDecl(n, src, exported)
	case "export_statement":
		return jsExportStmt(n, src)
	case "interface_declaration":
		if e := jsInterface(n, src, exported); e != nil {
			return []Entity{*e}
		}
	}
	return nil
}

func jsFunction(n *sitter.Node, src []byte, exported bool) *Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	return &Entity{
		Kind:     KindFunction,
		Name:     nameNode.Content(src),
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: exported,
	}
}

func jsClass(n *sitter.Node, src []byte, exported bool) []Entity {
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
		Exported: exported,
	}}

	body := n.ChildByFieldName("body")
	if body != nil {
		entities = append(entities, jsClassMethods(body, src, name)...)
	}
	return entities
}

func jsClassMethods(body *sitter.Node, src []byte, className string) []Entity {
	var methods []Entity
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		if child.Type() != "method_definition" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		methods = append(methods, Entity{
			Kind:     KindMethod,
			Name:     nameNode.Content(src),
			StartRow: child.StartPoint().Row,
			EndRow:   child.EndPoint().Row,
			Exported: true, // Class methods are part of the class's public surface.
			Receiver: className,
		})
	}
	return methods
}

func jsLexicalDecl(n *sitter.Node, src []byte, exported bool) []Entity {
	var entities []Entity
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child.Type() != "variable_declarator" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		// Check if value is an arrow function.
		value := child.ChildByFieldName("value")
		kind := KindConstant
		if value != nil && value.Type() == "arrow_function" {
			kind = KindFunction
		}
		entities = append(entities, Entity{
			Kind:     kind,
			Name:     nameNode.Content(src),
			StartRow: child.StartPoint().Row,
			EndRow:   child.EndPoint().Row,
			Exported: exported,
		})
	}
	return entities
}

func jsExportStmt(n *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		entities = append(entities, jsNode(child, src, true)...)
	}
	return entities
}

func jsInterface(n *sitter.Node, src []byte, exported bool) *Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	return &Entity{
		Kind:     KindInterface,
		Name:     nameNode.Content(src),
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: exported,
	}
}

// extractJSImports extracts import declarations from JS/TS source.
func extractJSImports(root *sitter.Node, src []byte) []Import {
	var imports []Import
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child.Type() != "import_statement" {
			continue
		}
		sourceNode := child.ChildByFieldName("source")
		if sourceNode == nil {
			continue
		}
		path := strings.Trim(sourceNode.Content(src), `"'`)
		imports = append(imports, Import{Path: path})
	}
	return imports
}
