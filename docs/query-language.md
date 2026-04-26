# Query Language

pcke includes a structured query DSL for searching the knowledge base.

## Grammar

```
query      = expr
expr       = or_expr
or_expr    = and_expr ("OR" and_expr)*
and_expr   = not_expr ("AND" not_expr)*
not_expr   = "NOT" not_expr | primary
primary    = comparison | "(" expr ")"
comparison = field operator value
field      = IDENT
operator   = "=" | "!=" | "<" | ">" | "<=" | ">=" | "CONTAINS" | "STARTS_WITH"
value      = STRING | NUMBER | BOOLEAN
```

## Fields

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Entity type: `"module"`, `"function"`, `"class"`, etc. |
| `name` | string | Entity name |
| `file` | string | Source file path |
| `module` | string | Module name |
| `tags` | string | Comma-separated tags |
| `language` | string | Programming language |

## Examples

### Simple equality

```
pcke query 'type = "module"'
```

### Compound queries

```
pcke query 'type = "function" AND module = "auth"'
pcke query 'language = "go" OR language = "python"'
pcke query 'NOT type = "test"'
```

### Contains and prefix

```
pcke query 'tags CONTAINS "security"'
pcke query 'name STARTS_WITH "Handle"'
```

## Query plan

Use `pcke explain` to see the execution plan without running the query:

```bash
pcke explain 'type = "module" AND tags CONTAINS "auth"'
```

## Export results

```bash
pcke export 'type = "module"' --format=json
pcke export 'type = "module"' --format=yaml
```
