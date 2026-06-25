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

// legacyKnPrefix is the byte prefix of v0.9.x knowledge-node records.
const legacyKnPrefix = "kn:"

// knBatchSize bounds the number of records translated per write
// transaction; kdb's CoW page allocation needs continuous headroom and
// one fat transaction can exhaust the freelist on large repos.
const knBatchSize = 100

// pagesPerMigratedRecord is the freelist headroom reserved per record in
// a bulk translation batch. A Link writes both a forward (l:) and a
// reverse-index (lr:) record and may split B+tree nodes under CoW, so
// the reservation is generous to keep a batch from running the freelist
// dry mid-transaction.
const pagesPerMigratedRecord = 8

// legacyKnowledgeNode mirrors the JSON shape of a v0.9.x kn: record.
// We keep this declared inside the migrate package (rather than
// depending on internal/analysis) so the migration is self-contained
// and unaffected by future scanner refactors.
type legacyKnowledgeNode struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	FilePath  string    `json:"file_path"`
	CreatedAt time.Time `json:"created_at"`
}

// V0011MigrateKnToE returns the migration that translates legacy
// kn: knowledge-node records into typed Entity (e:) events.
//
// Translation policy:
//   - Each kn:<id> JSON record becomes one Entity at e:<id>:v1.
//   - Header.CreatedAt is preserved from the legacy record.
//   - Header.Lifecycle is set to Active.
//   - Type/Path/Name fields are mapped 1:1.
//   - Other legacy fields (Language, Module, Stability, Status,
//     ContentHash, Entities[], Imports[]) are intentionally dropped
//     for v0.10.0; the new Entity schema is deliberately lean.
//
// Idempotency: each translated record is keyed at e:<id>:v1. Before
// writing, the migration checks whether v1 already exists and skips if
// so, so a partial run that resumes will not produce v2/v3 duplicates.
//
// The legacy kn: records are NOT removed; they remain readable for
// backward compatibility until a future migration retires them.
func V0011MigrateKnToE() Migration {
	return Migration{
		Version:     11,
		Description: "translate legacy kn: knowledge-node records to typed Entity events",
		Migrate:     migrateKnToE,
	}
}

func migrateKnToE(ctx context.Context, db UpdateDB) error {
	records, err := collectLegacyKnRecords(ctx, db)
	if err != nil {
		return fmt.Errorf("migrate 0011: collect: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	store := event.New(db)
	for start := 0; start < len(records); start += knBatchSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("migrate 0011: cancelled: %w", err)
		}
		end := start + knBatchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[start:end]
		if err := ensureFreePages(db, len(batch)*pagesPerMigratedRecord); err != nil {
			return fmt.Errorf("migrate 0011: grow: %w", err)
		}
		if err := translateKnBatch(ctx, db, store, batch); err != nil {
			return fmt.Errorf("migrate 0011: batch %d-%d: %w", start, end, err)
		}
	}
	return nil
}

// legacyKnRecord is the in-memory tuple captured during the read phase.
// We deliberately copy bytes so the values survive the transaction
// boundary into the write phase.
type legacyKnRecord struct {
	key   []byte
	value []byte
}

func collectLegacyKnRecords(ctx context.Context, db UpdateDB) ([]legacyKnRecord, error) {
	var records []legacyKnRecord
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		if !cursor.Seek([]byte(legacyKnPrefix)) {
			return nil
		}
		for cursor.Valid() {
			key := cursor.Key()
			if !bytes.HasPrefix(key, []byte(legacyKnPrefix)) {
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

func translateKnBatch(ctx context.Context, db UpdateDB, store *event.Store, batch []legacyKnRecord) error {
	return db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, r := range batch {
			if err := translateKnRecord(wtx, store, r); err != nil {
				return err
			}
		}
		return nil
	})
}

func translateKnRecord(wtx *tx.WriteTx, store *event.Store, r legacyKnRecord) error {
	var node legacyKnowledgeNode
	if err := json.Unmarshal(r.value, &node); err != nil {
		return fmt.Errorf("decode %q: %w", r.key, err)
	}
	if node.ID == "" {
		return nil
	}

	// Idempotency: skip records already translated to e:<id>:v1.
	expectedKey, err := event.BuildKey(event.KindEntity, node.ID, 1)
	if err != nil {
		return fmt.Errorf("build key for %q: %w", node.ID, err)
	}
	if _, getErr := wtx.Get(expectedKey); getErr == nil {
		return nil
	} else if !errors.Is(getErr, btree.ErrKeyNotFound) {
		return fmt.Errorf("probe %q: %w", expectedKey, getErr)
	}

	entity := &event.Entity{
		Hdr: event.Header{
			CreatedAt: node.CreatedAt,
			Lifecycle: event.LifecycleActive,
		},
		EID:  node.ID,
		Type: node.Type,
		Path: node.FilePath,
		Name: node.Name,
	}
	if _, err := store.AppendInTx(wtx, entity); err != nil {
		return fmt.Errorf("append e:%s: %w", node.ID, err)
	}
	return nil
}
