package query

import (
	"fmt"
	"sort"
	"sync"
)

// FieldType describes the data type of a collection field. The type checker
// uses this to validate operator compatibility in WHERE conditions.
type FieldType int

// Field type constants.
const (
	FieldString      FieldType = iota // plain string
	FieldNumber                       // int or float
	FieldBool                         // true/false
	FieldTime                         // timestamp (compared as string, ISO 8601)
	FieldStringSlice                  // []string (supports "contains")
)

// Schema maps field names to their types for a given collection.
type Schema map[string]FieldType

// FieldNames returns the sorted field names in the schema.
func (s Schema) FieldNames() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// String returns the human-readable name of a field type.
func (ft FieldType) String() string {
	switch ft {
	case FieldString:
		return "string"
	case FieldNumber:
		return "number"
	case FieldBool:
		return "bool"
	case FieldTime:
		return "time"
	case FieldStringSlice:
		return "[]string"
	default:
		return "unknown"
	}
}

// ParseFieldType converts a string type name to a FieldType. Returns an error
// if the type name is not recognized.
func ParseFieldType(s string) (FieldType, error) {
	switch s {
	case "string":
		return FieldString, nil
	case "number", "int", "float":
		return FieldNumber, nil
	case "bool":
		return FieldBool, nil
	case "time":
		return FieldTime, nil
	case "[]string":
		return FieldStringSlice, nil
	default:
		return 0, fmt.Errorf("query: unknown field type %q", s)
	}
}

// mu guards mutations to the collections map (RegisterField, RegisterCollection).
var mu sync.RWMutex

// collections defines the known schema for each queryable collection.
// These correspond to the JSON-serialized structs stored in kdb.
var collections = map[string]Schema{
	"nodes": {
		"id":           FieldString,
		"type":         FieldString,
		"name":         FieldString,
		"file_path":    FieldString,
		"language":     FieldString,
		"module":       FieldString,
		"class":        FieldString,
		"stability":    FieldNumber,
		"status":       FieldString,
		"content_hash": FieldString,
		"created_at":   FieldTime,
		"updated_at":   FieldTime,
	},
	"evolution": {
		"id":          FieldString,
		"node_id":     FieldString,
		"commit_hash": FieldString,
		"change_type": FieldString,
		"author":      FieldString,
		"timestamp":   FieldTime,
	},
	"constraints": {
		"id":          FieldString,
		"scope":       FieldString,
		"severity":    FieldString,
		"description": FieldString,
		"created_at":  FieldTime,
	},
	"notes": {
		"id":         FieldString,
		"tags":       FieldStringSlice,
		"content":    FieldString,
		"created_at": FieldTime,
		"updated_at": FieldTime,
	},
	"relations": {
		"id":             FieldString,
		"source_node_id": FieldString,
		"target_node_id": FieldString,
		"type":           FieldString,
		"source":         FieldString,
		"created_at":     FieldTime,
	},
}

// CollectionSchema returns the schema for a collection, or nil if unknown.
func CollectionSchema(name string) Schema {
	mu.RLock()
	defer mu.RUnlock()
	return collections[name]
}

// Collections returns the names of all known collections.
func Collections() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(collections))
	for name := range collections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RegisterField adds a field to an existing collection schema. Returns an
// error if the collection does not exist. If the field already exists with
// the same type, it is a no-op (idempotent). If the field exists with a
// different type, an error is returned.
func RegisterField(collection, field string, ft FieldType) error {
	mu.Lock()
	defer mu.Unlock()

	schema, ok := collections[collection]
	if !ok {
		return fmt.Errorf("query: unknown collection %q", collection)
	}

	if existing, exists := schema[field]; exists {
		if existing == ft {
			return nil // idempotent
		}
		return fmt.Errorf("query: field %q already exists in %q with type %s (requested %s)",
			field, collection, existing, ft)
	}

	schema[field] = ft
	return nil
}

// RegisterCollection adds a new collection to the schema registry. Returns
// an error if the collection already exists. If the collection already exists
// with an identical schema, it is a no-op (idempotent).
func RegisterCollection(name string, schema Schema) error {
	mu.Lock()
	defer mu.Unlock()

	if existing, ok := collections[name]; ok {
		if schemasEqual(existing, schema) {
			return nil // idempotent
		}
		return fmt.Errorf("query: collection %q already exists", name)
	}

	// Copy the schema to avoid external mutation.
	cp := make(Schema, len(schema))
	for k, v := range schema {
		cp[k] = v
	}
	collections[name] = cp
	return nil
}

// schemasEqual returns true if two schemas have the same fields and types.
func schemasEqual(a, b Schema) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
