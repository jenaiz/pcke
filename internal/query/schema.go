package query

import "sort"

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
	return collections[name]
}

// Collections returns the names of all known collections.
func Collections() []string {
	names := make([]string, 0, len(collections))
	for name := range collections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
