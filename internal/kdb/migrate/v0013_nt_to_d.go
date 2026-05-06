package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// legacyNtPrefix is the byte prefix of v0.9.x note records.
const legacyNtPrefix = "nt:"

// legacyNote mirrors the JSON shape of a v0.9.x nt: record. The fields
// are stored as a generic map by the original `pcke note add` command;
// the JSON tags here pin the names. Unknown fields are silently
// ignored on decode.
type legacyNote struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"` // RFC3339 string in legacy format
}

// V0013MigrateNtToD returns the migration that translates legacy
// nt: note records into typed Decision (d:) events.
//
// Translation policy:
//   - DID = legacy.id (typically a UUID).
//   - Title = first non-empty line of content, truncated to 200 chars
//     for legibility in indexes; full content goes in Body.
//   - Body = legacy.content unchanged.
//   - Severity = SeverityShould — legacy notes carried no severity
//     field; "should" is the conservative default for unstructured
//     advice.
//   - Scope = ScopeGlobal — legacy notes weren't scoped to a file or
//     module; they apply repo-wide.
//   - Source = "manual" — these were user-created via `pcke note add`.
//   - Header.CreatedAt = parsed legacy.created_at (RFC3339); falls
//     back to time.Time{} on parse failure.
//   - Header.Lifecycle = Active.
//
// Tags from the legacy record are dropped; the v0.10.0 Decision schema
// does not carry tags. They can be reintroduced as a payload field in
// a future schema bump if needed.
//
// Idempotency: each Decision is keyed at d:<id>:v1; the migration
// skips records whose v1 key already exists. Legacy nt: records are
// preserved.
func V0013MigrateNtToD() Migration {
	return Migration{
		Version:     13,
		Description: "translate legacy nt: note records to typed Decision events",
		Migrate:     migrateNtToD,
	}
}

func migrateNtToD(ctx context.Context, db UpdateDB) error {
	records, err := collectLegacyNtRecords(ctx, db)
	if err != nil {
		return fmt.Errorf("migrate 0013: collect: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	store := event.New(db)
	for start := 0; start < len(records); start += knBatchSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("migrate 0013: cancelled: %w", err)
		}
		end := start + knBatchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[start:end]
		if err := translateNtBatch(ctx, db, store, batch); err != nil {
			return fmt.Errorf("migrate 0013: batch %d-%d: %w", start, end, err)
		}
	}
	return nil
}

func collectLegacyNtRecords(ctx context.Context, db UpdateDB) ([]legacyKnRecord, error) {
	var records []legacyKnRecord
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		if !cursor.Seek([]byte(legacyNtPrefix)) {
			return nil
		}
		for cursor.Valid() {
			key := cursor.Key()
			if !bytes.HasPrefix(key, []byte(legacyNtPrefix)) {
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

func translateNtBatch(ctx context.Context, db UpdateDB, store *event.Store, batch []legacyKnRecord) error {
	return db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, r := range batch {
			if err := translateNtRecord(wtx, store, r); err != nil {
				return err
			}
		}
		return nil
	})
}

func translateNtRecord(wtx *tx.WriteTx, store *event.Store, r legacyKnRecord) error {
	var note legacyNote
	if err := json.Unmarshal(r.value, &note); err != nil {
		return fmt.Errorf("decode %q: %w", r.key, err)
	}
	if note.ID == "" {
		return nil
	}

	createdAt, _ := time.Parse(time.RFC3339, note.CreatedAt) // zero on failure
	title := firstNonEmptyLine(note.Content, 200)

	dec := &event.Decision{
		Hdr: event.Header{
			CreatedAt: createdAt,
			Lifecycle: event.LifecycleActive,
		},
		DID:      note.ID,
		Title:    title,
		Body:     note.Content,
		Severity: event.SeverityShould,
		Scope:    event.ScopeGlobal,
		Source:   "manual",
	}

	expectedKey, err := event.BuildKey(event.KindDecision, note.ID, 1)
	if err != nil {
		return fmt.Errorf("build key for %q: %w", note.ID, err)
	}
	if _, getErr := wtx.Get(expectedKey); getErr == nil {
		return nil
	} else if !errors.Is(getErr, btree.ErrKeyNotFound) {
		return fmt.Errorf("probe %q: %w", expectedKey, getErr)
	}

	if _, err := store.AppendInTx(wtx, dec); err != nil {
		return fmt.Errorf("append d:%s: %w", note.ID, err)
	}
	return nil
}

// firstNonEmptyLine returns the first non-empty trimmed line of s,
// truncated to maxLen runes. Returns "(untitled)" if s has no content.
func firstNonEmptyLine(s string, maxLen int) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if r := []rune(trimmed); len(r) > maxLen {
			return string(r[:maxLen])
		}
		return trimmed
	}
	return "(untitled)"
}
