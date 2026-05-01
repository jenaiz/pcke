//go:build cgo

package ast

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractJavaEntities walks the Java AST and extracts classes, interfaces,
// enums, annotation types, methods, and static final constants.
func extractJavaEntities(root *sitter.Node, src []byte) []Entity {
	var entities []Entity
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "class_declaration":
			entities = append(entities, javaClass(child, src)...)
		case "interface_declaration":
			entities = append(entities, javaInterface(child, src)...)
		case "enum_declaration":
			entities = append(entities, javaEnum(child, src))
		case "annotation_type_declaration":
			entities = append(entities, javaAnnotationType(child, src))
		}
	}
	return entities
}

// javaClass extracts a class entity and its methods/constants from a class_declaration.
func javaClass(n *sitter.Node, src []byte) []Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	exported := javaHasModifier(n, "public")

	entities := []Entity{{
		Kind:     KindClass,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: exported,
		Doc:      javaLeadingComment(n, src),
	}}

	body := n.ChildByFieldName("body")
	if body != nil {
		entities = append(entities, javaClassMembers(body, src, name)...)
	}
	return entities
}

// javaInterface extracts an interface entity and its method declarations.
func javaInterface(n *sitter.Node, src []byte) []Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)

	entities := []Entity{{
		Kind:     KindInterface,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: javaHasModifier(n, "public"),
		Doc:      javaLeadingComment(n, src),
	}}

	body := n.ChildByFieldName("body")
	if body != nil {
		entities = append(entities, javaInterfaceMethods(body, src, name)...)
	}
	return entities
}

// javaEnum extracts an enum entity from an enum_declaration.
func javaEnum(n *sitter.Node, src []byte) Entity {
	nameNode := n.ChildByFieldName("name")
	name := ""
	if nameNode != nil {
		name = nameNode.Content(src)
	}
	return Entity{
		Kind:     KindEnum,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: javaHasModifier(n, "public"),
		Doc:      javaLeadingComment(n, src),
	}
}

// javaAnnotationType extracts an annotation type entity from an
// annotation_type_declaration.
func javaAnnotationType(n *sitter.Node, src []byte) Entity {
	nameNode := n.ChildByFieldName("name")
	name := ""
	if nameNode != nil {
		name = nameNode.Content(src)
	}
	return Entity{
		Kind:     KindAnnotation,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: javaHasModifier(n, "public"),
		Doc:      javaLeadingComment(n, src),
	}
}

// javaClassMembers extracts methods, constructors, and static final constants
// from a class_body node.
func javaClassMembers(body *sitter.Node, src []byte, className string) []Entity {
	var entities []Entity
	for i := 0; i < int(body.NamedChildCount()); i++ {
		member := body.NamedChild(i)
		switch member.Type() {
		case "method_declaration":
			if e := javaMethod(member, src, className); e != nil {
				entities = append(entities, *e)
			}
		case "constructor_declaration":
			if e := javaConstructor(member, src, className); e != nil {
				entities = append(entities, *e)
			}
		case "field_declaration":
			if e := javaStaticFinalField(member, src); e != nil {
				entities = append(entities, *e)
			}
		case "class_declaration":
			// First-level inner classes.
			entities = append(entities, javaInnerClass(member, src, className)...)
		}
	}
	return entities
}

// javaInterfaceMethods extracts method declarations from an interface_body.
func javaInterfaceMethods(body *sitter.Node, src []byte, ifaceName string) []Entity {
	var entities []Entity
	for i := 0; i < int(body.NamedChildCount()); i++ {
		member := body.NamedChild(i)
		if member.Type() != "method_declaration" {
			continue
		}
		if e := javaMethod(member, src, ifaceName); e != nil {
			entities = append(entities, *e)
		}
	}
	return entities
}

// javaMethod extracts a method entity from a method_declaration.
func javaMethod(n *sitter.Node, src []byte, receiver string) *Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	return &Entity{
		Kind:     KindMethod,
		Name:     nameNode.Content(src),
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: javaHasModifier(n, "public"),
		Receiver: receiver,
		Doc:      javaLeadingComment(n, src),
	}
}

// javaConstructor extracts a constructor entity from a constructor_declaration.
func javaConstructor(n *sitter.Node, src []byte, className string) *Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	return &Entity{
		Kind:     KindMethod,
		Name:     nameNode.Content(src),
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: javaHasModifier(n, "public"),
		Receiver: className,
		Doc:      javaLeadingComment(n, src),
	}
}

// javaStaticFinalField extracts a constant entity from a field_declaration
// that has both "static" and "final" modifiers.
func javaStaticFinalField(n *sitter.Node, src []byte) *Entity {
	if !javaHasModifier(n, "static") || !javaHasModifier(n, "final") {
		return nil
	}

	// Find the variable_declarator for the field name.
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child.Type() == "variable_declarator" {
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			return &Entity{
				Kind:     KindConstant,
				Name:     nameNode.Content(src),
				StartRow: n.StartPoint().Row,
				EndRow:   n.EndPoint().Row,
				Exported: javaHasModifier(n, "public"),
			}
		}
	}
	return nil
}

// javaInnerClass extracts a first-level inner class with qualified name.
func javaInnerClass(n *sitter.Node, src []byte, outerName string) []Entity {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := outerName + "." + nameNode.Content(src)
	exported := javaHasModifier(n, "public")

	entities := []Entity{{
		Kind:     KindClass,
		Name:     name,
		StartRow: n.StartPoint().Row,
		EndRow:   n.EndPoint().Row,
		Exported: exported,
	}}

	body := n.ChildByFieldName("body")
	if body != nil {
		entities = append(entities, javaClassMembers(body, src, name)...)
	}
	return entities
}

// javaHasModifier checks whether a declaration node has the given modifier
// (e.g. "public", "static", "final").
func javaHasModifier(n *sitter.Node, modifier string) bool {
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child.Type() == "modifiers" {
			// Scan modifier children for the exact keyword.
			for j := 0; j < int(child.ChildCount()); j++ {
				if child.Child(j).Type() == modifier {
					return true
				}
			}
			return false
		}
	}
	return false
}

// javaLeadingComment extracts a doc comment (block or line) directly above
// a declaration node.
func javaLeadingComment(n *sitter.Node, src []byte) string {
	prev := n.PrevNamedSibling()
	if prev == nil {
		return ""
	}

	// Java uses block_comment for /** ... */ Javadoc and line_comment for //.
	switch prev.Type() {
	case "block_comment", "line_comment":
		// Only if it immediately precedes the declaration.
		if n.StartPoint().Row-prev.EndPoint().Row > 1 {
			return ""
		}
		text := prev.Content(src)
		text = strings.TrimPrefix(text, "/**")
		text = strings.TrimPrefix(text, "/*")
		text = strings.TrimPrefix(text, "//")
		text = strings.TrimSuffix(text, "*/")
		text = strings.TrimSpace(text)
		return text
	default:
		return ""
	}
}

// extractJavaImports extracts import and import static declarations from
// Java source.
func extractJavaImports(root *sitter.Node, src []byte) []Import {
	var imports []Import
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child.Type() != "import_declaration" {
			continue
		}
		imp := javaImportDecl(child, src)
		if imp != nil {
			imports = append(imports, *imp)
		}
	}
	return imports
}

// javaImportDecl extracts the import path from an import_declaration.
// For "import java.util.List;" → path "java.util.List".
// For "import static java.util.Collections.emptyList;" → path with
// "static:" prefix.
func javaImportDecl(n *sitter.Node, src []byte) *Import {
	isStatic := false
	var path string

	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		switch {
		case child.Type() == "static":
			isStatic = true
		case child.IsNamed() && (child.Type() == "scoped_identifier" || child.Type() == "identifier"):
			path = child.Content(src)
		case child.Type() == "asterisk":
			path += ".*"
		}
	}

	if path == "" {
		return nil
	}

	alias := ""
	if isStatic {
		alias = "static"
	}

	return &Import{Path: path, Alias: alias}
}
