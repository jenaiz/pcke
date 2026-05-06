package migrate_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

func seedLegacyEl(t *testing.T, db *kdb.DB, ids []string) {
	t.Helper()
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		for _, id := range ids {
			data := []byte(`{"id":"` + id + `","change_type":"rename"}`)
			if err := wtx.Put([]byte("el:"+id), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed el: %v", err)
	}
}

func runEngineTo14(t *testing.T, db *kdb.DB) {
	t.Helper()
	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	e.Register(migrate.V0012MigrateRelToL())
	e.Register(migrate.V0013MigrateNtToD())
	e.Register(migrate.V0014RetireEvolutionLog())
	if _, err := e.Run(context.Background(), db); err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
}

func countElRecords(t *testing.T, db *kdb.DB) int {
	t.Helper()
	count := 0
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		if !cursor.Seek([]byte("el:")) {
			return nil
		}
		for cursor.Valid() {
			if !bytes.HasPrefix(cursor.Key(), []byte("el:")) {
				break
			}
			count++
			cursor.Next()
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
	return count
}

func TestV0014_RemovesAllElRecords(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	ids := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		ids = append(ids, fmt.Sprintf("evt-%03d", i))
	}
	seedLegacyEl(t, db, ids)

	if before := countElRecords(t, db); before != 50 {
		t.Fatalf("seeded el count = %d, want 50", before)
	}
	runEngineTo14(t, db)
	if after := countElRecords(t, db); after != 0 {
		t.Errorf("post-migration el count = %d, want 0", after)
	}
	if got := db.SchemaVersion(); got != 14 {
		t.Errorf("schema version = %d, want 14", got)
	}
}

func TestV0014_NoElRecordsIsNoOp(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)
	runEngineTo14(t, db)
	if got := db.SchemaVersion(); got != 14 {
		t.Errorf("schema version = %d, want 14", got)
	}
	if c := countElRecords(t, db); c != 0 {
		t.Errorf("el count = %d, want 0", c)
	}
}

func TestV0014_IsIdempotent(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	seedLegacyEl(t, db, []string{"a", "b"})
	runEngineTo14(t, db)
	if c := countElRecords(t, db); c != 0 {
		t.Fatalf("first run left %d el records, want 0", c)
	}

	// Second run: engine sees current version 14, skips entirely.
	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	e.Register(migrate.V0012MigrateRelToL())
	e.Register(migrate.V0013MigrateNtToD())
	e.Register(migrate.V0014RetireEvolutionLog())
	applied, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if applied != 0 {
		t.Errorf("re-run applied = %d, want 0", applied)
	}
}

func TestV0014_LeavesOtherPrefixesUntouched(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	// Seed some el: + a kn: record. Migration must not touch kn:.
	seedLegacyEl(t, db, []string{"x"})
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("kn:keep-me"), []byte(`{"id":"keep-me","type":"file"}`))
	}); err != nil {
		t.Fatalf("seed kn: %v", err)
	}

	runEngineTo14(t, db)

	// el: gone.
	if c := countElRecords(t, db); c != 0 {
		t.Errorf("el count = %d, want 0", c)
	}

	// kn: still present.
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		_, err := rtx.Get([]byte("kn:keep-me"))
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		if errors.Is(err, btree.ErrKeyNotFound) {
			t.Errorf("kn:keep-me was unexpectedly deleted")
		} else {
			t.Errorf("View: %v", err)
		}
	}
}
