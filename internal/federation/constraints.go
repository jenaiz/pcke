package federation

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Violation describes a repo violating an org-wide constraint.
type Violation struct {
	Repo        string
	Constraint  OrgConstraint
	NodeID      string
	Description string
}

// CheckOrgConstraints checks the local repo for violations of org-wide constraints.
// Currently checks:
// - scope "all": scans all nodes for pattern violations
// - scope "api": checks nodes in modules tagged as api
func CheckOrgConstraints(ctx context.Context, db *kdb.DB, manifest *Manifest) ([]Violation, error) {
	if len(manifest.Constraints.Rules) == 0 {
		return nil, nil
	}

	var violations []Violation
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		prefix := []byte("kn:")
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "kn:") {
				break
			}
			v := cursor.Value()

			var node map[string]any
			if err := json.Unmarshal(v, &node); err != nil {
				continue
			}

			for _, rule := range manifest.Constraints.Rules {
				if viol := checkNodeViolation(node, rule); viol != nil {
					violations = append(violations, *viol)
				}
			}
		}
		return nil
	})
	return violations, err
}

// checkNodeViolation checks if a single node violates a constraint.
func checkNodeViolation(node map[string]any, rule OrgConstraint) *Violation {
	module, _ := node["module"].(string)
	nodeID, _ := node["id"].(string)

	switch rule.Scope {
	case "all":
		// "all" scope constraints are advisory — they flag nodes that might
		// need review. For now, check if the description mentions patterns
		// we can detect (e.g., "No direct DB access").
		if strings.Contains(strings.ToLower(rule.Description), "no direct db access") {
			// Check if module references "database" or "sql" in a boundary-crossing way.
			typ, _ := node["type"].(string)
			if typ == "import" && containsAny(module, "sql", "database", "db") {
				return &Violation{
					Constraint:  rule,
					NodeID:      nodeID,
					Description: "Direct DB access detected in " + module,
				}
			}
		}
	case "api":
		// Only applies to modules tagged "api" or containing "api" in name.
		if !strings.Contains(strings.ToLower(module), "api") {
			return nil
		}
		// For API scope, flag if constraint mentions annotations and node lacks them.
		if strings.Contains(strings.ToLower(rule.Description), "annotation") {
			annotations, _ := node["annotations"].([]any)
			if len(annotations) == 0 {
				return &Violation{
					Constraint:  rule,
					NodeID:      nodeID,
					Description: "API node missing required annotations in " + module,
				}
			}
		}
	}
	return nil
}

// PropagateConstraints writes org constraints from the manifest into the local DB
// so they are visible during local queries.
func PropagateConstraints(ctx context.Context, db *kdb.DB, manifest *Manifest) error {
	return db.Update(ctx, func(wtx *tx.WriteTx) error {
		// Clear existing org constraints.
		prefix := []byte("oc:")
		cursor := wtx.Cursor()
		var toDelete [][]byte
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "oc:") {
				break
			}
			toDelete = append(toDelete, append([]byte(nil), k...))
		}
		for _, k := range toDelete {
			if err := wtx.Delete(k); err != nil {
				return err
			}
		}

		// Write manifest constraints.
		for i, rule := range manifest.Constraints.Rules {
			key := []byte(strings.Join([]string{"oc:", rule.Scope, ":", itoa(i)}, ""))
			val, _ := json.Marshal(map[string]string{
				"scope":       rule.Scope,
				"severity":    rule.Severity,
				"description": rule.Description,
			})
			if err := wtx.Put(key, val); err != nil {
				return err
			}
		}
		return nil
	})
}

func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	return strings.Repeat("0", max(0, 4-len(intStr(i)))) + intStr(i)
}

func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
