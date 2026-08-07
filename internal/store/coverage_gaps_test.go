package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// NOTE on clusters.go's remaining uncovered branches: generateClusterID's
// rand.Read error path (and its propagation in CreateCluster) is dead code under
// this Go toolchain — crypto/rand.Read no longer returns a non-nil error; it
// aborts the process via fatal() (golang.org/issue/66821). The json.Marshal
// error returns in marshalClusterFields are likewise unreachable: every cluster
// field is a concrete struct of strings/ints/bools/slices/pointers, none of
// which can fail to marshal. Neither is testable without a production seam.

// TestStorePing verifies Ping reports a healthy connection on an open store and
// an error once the underlying DB is closed.
func TestStorePing(t *testing.T) {
	s := testStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping on open store = %v, want nil", err)
	}

	// After closing the DB, Ping must report the connection is unusable.
	s.db.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Error("Ping on closed store = nil, want error")
	}
}

// TestMetricsWriterDroppedCounter fills the writer's buffer so the next Submit is
// dropped, then asserts Dropped() reflects the lost sample. Deterministic: Run()
// is never started, so the buffer stays full and the counter is read after the
// overflow Submit returns.
func TestMetricsWriterDroppedCounter(t *testing.T) {
	s := testStore(t)
	// Do NOT start Run() — the channel must stay full so Submit drops.
	w := NewMetricsWriter(s, slog.Default(), 0)

	if got := w.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d before any drop, want 0", got)
	}

	sample := MetricSample{Timestamp: time.Now(), Env: "drop-env"}

	// Fill the buffer exactly to capacity; none of these should be dropped.
	capacity := cap(w.ch)
	for i := 0; i < capacity; i++ {
		w.Submit(sample)
	}
	if got := w.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d after filling buffer to capacity, want 0", got)
	}

	// The next Submit overflows the buffer and must be counted as dropped.
	w.Submit(sample)
	if got := w.Dropped(); got != 1 {
		t.Errorf("Dropped() = %d after one overflow Submit, want 1", got)
	}

	// A further overflow Submit increments the cumulative counter again.
	w.Submit(sample)
	if got := w.Dropped(); got != 2 {
		t.Errorf("Dropped() = %d after two overflow Submits, want 2", got)
	}
}

func TestIncrementalAutoVacuumFailurePaths(t *testing.T) {
	t.Run("connection", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		db.Close()
		if ensureIncrementalAutoVacuum(db) == nil {
			t.Fatal("expected connection failure")
		}
	})

	t.Run("query", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectQuery("PRAGMA auto_vacuum").WillReturnError(errInjected)
		if ensureIncrementalAutoVacuum(db) == nil {
			t.Fatal("expected auto-vacuum query failure")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("configure", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectQuery("PRAGMA auto_vacuum").WillReturnRows(sqlmock.NewRows([]string{"auto_vacuum"}).AddRow(0))
		mock.ExpectExec("PRAGMA auto_vacuum = INCREMENTAL").WillReturnError(errInjected)
		if ensureIncrementalAutoVacuum(db) == nil {
			t.Fatal("expected auto-vacuum configuration failure")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("vacuum", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectQuery("PRAGMA auto_vacuum").WillReturnRows(sqlmock.NewRows([]string{"auto_vacuum"}).AddRow(0))
		mock.ExpectExec("PRAGMA auto_vacuum = INCREMENTAL").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("VACUUM").WillReturnError(errInjected)
		if ensureIncrementalAutoVacuum(db) == nil {
			t.Fatal("expected vacuum failure")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAdditionalStoreFailurePaths(t *testing.T) {
	t.Run("open path is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(filepath.Join(file, "child")); err == nil {
			t.Fatal("Open accepted a data path below a regular file")
		}
	})

	t.Run("backup inspection and database errors", func(t *testing.T) {
		s := testStore(t)
		if err := s.Backup(context.Background(), "bad\x00path"); err == nil {
			t.Fatal("Backup accepted an invalid destination path")
		}
		if err := s.db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Backup(context.Background(), filepath.Join(t.TempDir(), "backup.db")); err == nil {
			t.Fatal("Backup succeeded with a closed database")
		}
	})

	t.Run("seed empty and insert failure", func(t *testing.T) {
		s := testStore(t)
		if created, err := s.SeedClusters(nil); err != nil || created != 0 {
			t.Fatalf("empty seed = %d, %v", created, err)
		}
		if _, err := s.db.Exec(`CREATE TRIGGER reject_cluster BEFORE INSERT ON clusters BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SeedClusters([]config.Environment{{Name: "rejected"}}); err == nil {
			t.Fatal("SeedClusters ignored an insert failure")
		}
	})

	t.Run("close without lock", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectClose()
		if err := (&Store{db: db}).Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("external identity reload", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT id, username").WillReturnError(errInjected)
		if _, err := (&Store{db: db}).UpsertExternalUser("ldap", "subject", "user", "User", "user@example.com", RoleViewer); err == nil {
			t.Fatal("expected external identity reload failure")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestUserHashAndMigrationFailures(t *testing.T) {
	t.Run("oversized passwords", func(t *testing.T) {
		s := testStore(t)
		oversized := string(make([]byte, 73))
		if _, err := s.CreateUser("too-long", oversized, RoleViewer); err == nil {
			t.Fatal("CreateUser accepted a password bcrypt cannot hash")
		}
		u, err := s.CreateUser("change-too-long", "secure-password", RoleViewer)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ChangePassword(u.ID, "secure-password", oversized); err == nil {
			t.Fatal("ChangePassword accepted a password bcrypt cannot hash")
		}
	})

	t.Run("begin migration", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectBegin().WillReturnError(errInjected)
		if migrateUsersSchema(db) == nil {
			t.Fatal("expected migration begin failure")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("migrate users", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS users").WillReturnError(errInjected)
		mock.ExpectRollback()
		if migrateUsersSchema(db) == nil {
			t.Fatal("expected users migration failure")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAuthenticateEnforcesAccountLockout(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateUser("locked", "secure-password", RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE users SET failed_attempts = ?, last_failed_at = ? WHERE id = ?", maxFailedAttempts, time.Now(), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("locked", "secure-password"); err != ErrAccountLocked {
		t.Fatalf("Authenticate error = %v, want ErrAccountLocked", err)
	}
}
