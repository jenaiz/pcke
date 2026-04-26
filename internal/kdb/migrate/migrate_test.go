package migrate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/migrate"
)

// mockDB implements migrate.DB for testing.
type mockDB struct {
	version uint16
}

func (m *mockDB) SchemaVersion() uint16     { return m.version }
func (m *mockDB) SetSchemaVersion(v uint16) { m.version = v }

func TestEngine_RunEmpty(t *testing.T) {
	e := migrate.New()
	db := &mockDB{version: 0}

	n, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 0 {
		t.Errorf("applied = %d, want 0", n)
	}
}

func TestEngine_RunSequential(t *testing.T) {
	e := migrate.New()

	var order []uint16
	for _, v := range []uint16{1, 2, 3} {
		v := v
		e.Register(migrate.Migration{
			Version:     v,
			Description: "test migration",
			Migrate: func(_ context.Context, _ migrate.DB) error {
				order = append(order, v)
				return nil
			},
		})
	}

	db := &mockDB{version: 0}
	n, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 3 {
		t.Errorf("applied = %d, want 3", n)
	}
	if db.version != 3 {
		t.Errorf("version = %d, want 3", db.version)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("execution order = %v, want [1 2 3]", order)
	}
}

func TestEngine_Idempotent(t *testing.T) {
	e := migrate.New()

	count := 0
	e.Register(migrate.Migration{
		Version:     1,
		Description: "first",
		Migrate: func(_ context.Context, _ migrate.DB) error {
			count++
			return nil
		},
	})

	db := &mockDB{version: 0}

	// First run.
	n, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if n != 1 {
		t.Errorf("run 1 applied = %d, want 1", n)
	}

	// Second run — should be no-op.
	n, err = e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if n != 0 {
		t.Errorf("run 2 applied = %d, want 0", n)
	}
	if count != 1 {
		t.Errorf("migration ran %d times, want 1", count)
	}
}

func TestEngine_PartialApplication(t *testing.T) {
	e := migrate.New()

	e.Register(migrate.Migration{
		Version:     1,
		Description: "first",
		Migrate: func(_ context.Context, _ migrate.DB) error {
			return nil
		},
	})
	e.Register(migrate.Migration{
		Version:     2,
		Description: "fails",
		Migrate: func(_ context.Context, _ migrate.DB) error {
			return errors.New("boom")
		},
	})
	e.Register(migrate.Migration{
		Version:     3,
		Description: "third",
		Migrate: func(_ context.Context, _ migrate.DB) error {
			return nil
		},
	})

	db := &mockDB{version: 0}
	n, err := e.Run(context.Background(), db)
	if err == nil {
		t.Fatal("expected error")
	}
	if n != 1 {
		t.Errorf("applied = %d, want 1 (before failure)", n)
	}
	if db.version != 1 {
		t.Errorf("version = %d, want 1", db.version)
	}
}

func TestEngine_SkipsApplied(t *testing.T) {
	e := migrate.New()

	var ran []uint16
	for _, v := range []uint16{1, 2, 3} {
		v := v
		e.Register(migrate.Migration{
			Version:     v,
			Description: "test",
			Migrate: func(_ context.Context, _ migrate.DB) error {
				ran = append(ran, v)
				return nil
			},
		})
	}

	db := &mockDB{version: 2} // already at version 2
	n, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 1 {
		t.Errorf("applied = %d, want 1", n)
	}
	if len(ran) != 1 || ran[0] != 3 {
		t.Errorf("ran = %v, want [3]", ran)
	}
}

func TestEngine_SchemaVersionMismatch(t *testing.T) {
	e := migrate.New()
	e.Register(migrate.Migration{
		Version:     1,
		Description: "first",
		Migrate: func(_ context.Context, _ migrate.DB) error {
			return nil
		},
	})

	db := &mockDB{version: 5} // newer than code
	_, err := e.Run(context.Background(), db)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, migrate.ErrSchemaVersionMismatch) {
		t.Errorf("error = %v, want ErrSchemaVersionMismatch", err)
	}
}

func TestEngine_ConflictPanics(t *testing.T) {
	e := migrate.New()
	e.Register(migrate.Migration{
		Version:     1,
		Description: "first",
		Migrate:     func(_ context.Context, _ migrate.DB) error { return nil },
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate version")
		}
	}()

	e.Register(migrate.Migration{
		Version:     1,
		Description: "duplicate",
		Migrate:     func(_ context.Context, _ migrate.DB) error { return nil },
	})
}

func TestEngine_Pending(t *testing.T) {
	e := migrate.New()
	for _, v := range []uint16{3, 1, 2} {
		v := v
		e.Register(migrate.Migration{
			Version:     v,
			Description: "test",
			Migrate:     func(_ context.Context, _ migrate.DB) error { return nil },
		})
	}

	db := &mockDB{version: 1}
	pending := e.Pending(db)
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}
	if pending[0].Version != 2 || pending[1].Version != 3 {
		t.Errorf("pending versions = [%d, %d], want [2, 3]",
			pending[0].Version, pending[1].Version)
	}
}

func TestEngine_LatestVersion(t *testing.T) {
	e := migrate.New()
	if got := e.LatestVersion(); got != 0 {
		t.Errorf("empty engine: LatestVersion = %d, want 0", got)
	}

	e.Register(migrate.Migration{Version: 3, Description: "a", Migrate: func(_ context.Context, _ migrate.DB) error { return nil }})
	e.Register(migrate.Migration{Version: 1, Description: "b", Migrate: func(_ context.Context, _ migrate.DB) error { return nil }})

	if got := e.LatestVersion(); got != 3 {
		t.Errorf("LatestVersion = %d, want 3", got)
	}
}

func TestEngine_CancelledContext(t *testing.T) {
	e := migrate.New()
	e.Register(migrate.Migration{
		Version:     1,
		Description: "first",
		Migrate:     func(_ context.Context, _ migrate.DB) error { return nil },
	})
	e.Register(migrate.Migration{
		Version:     2,
		Description: "second",
		Migrate:     func(_ context.Context, _ migrate.DB) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	db := &mockDB{version: 0}
	_, err := e.Run(ctx, db)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
