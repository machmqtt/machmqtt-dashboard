package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestAutoVacuumIncrementalOnFreshDB verifies a newly-created store uses
// incremental auto_vacuum mode.
func TestAutoVacuumIncrementalOnFreshDB(t *testing.T) {
	s := testStore(t)
	var mode int
	if err := s.db.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum = %d, want 2 (INCREMENTAL)", mode)
	}
}

// TestAutoVacuumConvertsExistingDB verifies a database created before this
// setting (auto_vacuum=NONE, the SQLite default) is converted to incremental
// mode when opened by the store — the pre-1.0 upgrade path.
func TestAutoVacuumConvertsExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dashboard.db")

	// Create a legacy database at the store's path with default auto_vacuum=NONE
	// and some data so the conversion VACUUM has real work to do.
	legacy, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (1),(2),(3)"); err != nil {
		t.Fatal(err)
	}
	var mode int
	if err := legacy.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 0 {
		t.Fatalf("precondition: legacy auto_vacuum = %d, want 0 (NONE)", mode)
	}
	legacy.Close()

	// Opening it through the store must convert it in place.
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	if err := s.db.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum after open = %d, want 2 (INCREMENTAL)", mode)
	}
	// The pre-existing table must survive the conversion VACUUM.
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("legacy rows after conversion = %d, want 3", n)
	}
}
