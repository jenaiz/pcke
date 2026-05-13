package migrate

import "context"

// V0015SessionBaseline returns the migration that bumps the schema
// version to 15, marking the database as compatible with the Phase 14
// Observation sub-types (o:session:<uuid>, o:call:<uuid>) introduced by
// F14.T1 (PRD v5.2 §5.3).
//
// The migration is a pure version marker: it touches no records and
// never fails. Session/ToolCall observations and their edges
// (contains / served / belongs_to) are written by the async collector
// (F14.T2) and read by the CLI surfaces (F14.T4, F14.T5).
//
// The bump exists so older builds opening a database that already
// contains o:session/o:call records can detect the format and either
// refuse to write or skip those records cleanly.
func V0015SessionBaseline() Migration {
	return Migration{
		Version:     15,
		Description: "register Phase 14 observation sub-types (o:session, o:call)",
		Migrate: func(_ context.Context, _ UpdateDB) error {
			return nil
		},
	}
}
