package migrate

import "context"

// V0010EventBaseline returns the migration that bumps the schema version
// to 10, marking the database as compatible with the typed-event log
// introduced by F12.T1 (Phase 12, PRD v5.2).
//
// The migration is intentionally a pure version marker: it touches no
// records and never fails. The new event-log namespaces (e:, d:, l:,
// lr:, o:, x:) live alongside the legacy kn:/rel:/nt:/el: records
// without interfering with reads or writes of either set.
//
// F12.T6 (next phase task) will add data-translation migrations 0011–
// 0014 that convert legacy records into typed events; this baseline
// only signals that the build understands the new schema.
func V0010EventBaseline() Migration {
	return Migration{
		Version:     10,
		Description: "register typed-event log namespaces (e:/d:/l:/lr:/o:/x:)",
		Migrate: func(_ context.Context, _ DB) error {
			// No data churn. The schema-version bump is performed by
			// the engine after this function returns nil.
			return nil
		},
	}
}
