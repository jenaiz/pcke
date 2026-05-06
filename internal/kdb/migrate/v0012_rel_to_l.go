package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// legacyRelPrefix is the byte prefix of v0.9.x relation records.
const legacyRelPrefix = "rel:"

// legacyRelation mirrors the JSON shape of a v0.9.x rel: record.
type legacyRelation struct {
	ID           string    `json:"id"`
	SourceNodeID string    `json:"source_node_id"`
	TargetNodeID string    `json:"target_node_id"`
	Type         string    `json:"type"` // "imports", "calls", etc.
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

// V0012MigrateRelToL returns the migration that translates legacy
// rel: relation records into typed Link (l:) events with paired
// reverse-index (lr:) records.
//
// Translation policy:
//   - SrcRef = "e:" + SourceNodeID; DstRef = "e:" + TargetNodeID.
//     Legacy node IDs are file paths or hashes; prefixing them with
//     "e:" turns them into typed entity references that the new graph
//     traversal expects.
//   - EdgeType = legacy.Type ("imports", "calls", ...). Empty type is
//     normalised to "related".
//   - Header.CreatedAt is preserved from the legacy record.
//   - Header.Lifecycle = Active.
//   - The lr: paired record is written automatically by Store.AppendInTx
//     for KindLink, so reverse traversal works immediately after the
//     migration.
//
// Idempotency: each Link is keyed at l:<src:edge:dst>:v1. The migration
// skips records whose v1 key already exists, so a partial run that
// crashed mid-migration resumes cleanly without producing v2 duplicates.
//
// The legacy rel: records are NOT removed; they remain readable for
// backward compatibility until a future migration retires them.
func V0012MigrateRelToL() Migration {
	return Migration{
		Version:     12,
		Description: "translate legacy rel: relation records to typed Link events",
		Migrate:     migrateRelToL,
	}
}

func migrateRelToL(ctx context.Context, db UpdateDB) error {
	records, err := collectLegacyRelRecords(ctx, db)
	if err != nil {
		return fmt.Errorf("migrate 0012: collect: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	store := event.New(db)
	for start := 0; start < len(records); start += knBatchSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("migrate 0012: cancelled: %w", err)
		}
		end := start + knBatchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[start:end]
		if err := translateRelBatch(ctx, db, store, batch); err != nil {
			return fmt.Errorf("migrate 0012: batch %d-%d: %w", start, end, err)
		}
	}
	return nil
}

func collectLegacyRelRecords(ctx context.Context, db UpdateDB) ([]legacyKnRecord, error) {
	var records []legacyKnRecord
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		if !cursor.Seek([]byte(legacyRelPrefix)) {
			return nil
		}
		for cursor.Valid() {
			key := cursor.Key()
			if !bytes.HasPrefix(key, []byte(legacyRelPrefix)) {
				break
			}
			records = append(records, legacyKnRecord{
				key:   key,
				value: cursor.Value(),
			})
			cursor.Next()
		}
		return nil
	})
	return records, err
}

func translateRelBatch(ctx context.Context, db UpdateDB, store *event.Store, batch []legacyKnRecord) error {
	return db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, r := range batch {
			if err := translateRelRecord(wtx, store, r); err != nil {
				return err
			}
		}
		return nil
	})
}

func translateRelRecord(wtx *tx.WriteTx, store *event.Store, r legacyKnRecord) error {
	var rel legacyRelation
	if err := json.Unmarshal(r.value, &rel); err != nil {
		return fmt.Errorf("decode %q: %w", r.key, err)
	}
	if rel.SourceNodeID == "" || rel.TargetNodeID == "" {
		// Skip malformed records — a relation without endpoints is
		// meaningless. Could log; for now silent skip.
		return nil
	}
	edgeType := rel.Type
	if edgeType == "" {
		edgeType = "related"
	}

	link := &event.Link{
		Hdr: event.Header{
			CreatedAt: rel.CreatedAt,
			Lifecycle: event.LifecycleActive,
		},
		SrcRef:   "e:" + rel.SourceNodeID,
		EdgeType: edgeType,
		DstRef:   "e:" + rel.TargetNodeID,
	}

	// Idempotency check: probe for v1 of this link.
	expectedKey, err := event.BuildKey(event.KindLink, link.ID(), 1)
	if err != nil {
		return fmt.Errorf("build key for %q: %w", link.ID(), err)
	}
	if _, getErr := wtx.Get(expectedKey); getErr == nil {
		return nil
	} else if !errors.Is(getErr, btree.ErrKeyNotFound) {
		return fmt.Errorf("probe %q: %w", expectedKey, getErr)
	}

	if _, err := store.AppendInTx(wtx, link); err != nil {
		return fmt.Errorf("append link %s -> %s: %w", link.SrcRef, link.DstRef, err)
	}
	return nil
}
