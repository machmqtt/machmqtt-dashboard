package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

var errInjected = errors.New("injected database failure")

func mockWriter(t *testing.T) (*MetricsWriter, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewMetricsWriter(db, slog.New(slog.NewTextHandler(io.Discard, nil))), mock, func() { _ = db.Close() }
}

func expectEnvInsert(mock sqlmock.Sqlmock) *sqlmock.ExpectedExec {
	return mock.ExpectExec("INSERT INTO env_metrics").
		WithArgs(sqlmock.AnyArg(), "test", 0, 0, 0, float64(0), float64(0), float64(0), float64(0), uint32(0))
}

func TestMetricsWriterTransactionFailures(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(sqlmock.Sqlmock)
		sample MetricSample
	}{
		{"begin", func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(errInjected) }, MetricSample{Env: "test"}},
		{"environment insert", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			expectEnvInsert(m).WillReturnError(errInjected)
			m.ExpectRollback()
		}, MetricSample{Env: "test"}},
		{"server prepare", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			expectEnvInsert(m).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectPrepare("INSERT INTO server_metrics").WillReturnError(errInjected)
			m.ExpectRollback()
		}, MetricSample{Env: "test"}},
		{"server insert", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			expectEnvInsert(m).WillReturnResult(sqlmock.NewResult(1, 1))
			p := m.ExpectPrepare("INSERT INTO server_metrics")
			p.ExpectExec().WillReturnError(errInjected)
			m.ExpectRollback()
		}, MetricSample{Env: "test", Servers: []ServerMetricSample{{ServerID: "s1", Healthy: true}}}},
		{"MQTT prepare", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			expectEnvInsert(m).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectPrepare("INSERT INTO server_metrics")
			m.ExpectPrepare("INSERT INTO mqtt_bridge_metrics").WillReturnError(errInjected)
			m.ExpectRollback()
		}, MetricSample{Env: "test"}},
		{"MQTT insert", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			expectEnvInsert(m).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectPrepare("INSERT INTO server_metrics")
			p := m.ExpectPrepare("INSERT INTO mqtt_bridge_metrics")
			p.ExpectExec().WillReturnError(errInjected)
			m.ExpectRollback()
		}, MetricSample{Env: "test", MQTTBridges: []MQTTBridgeMetricSample{{BridgeID: "b1"}}}},
		{"commit", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			expectEnvInsert(m).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectPrepare("INSERT INTO server_metrics")
			m.ExpectPrepare("INSERT INTO mqtt_bridge_metrics")
			m.ExpectCommit().WillReturnError(errInjected)
		}, MetricSample{Env: "test"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, mock, closeDB := mockWriter(t)
			defer closeDB()
			tc.setup(mock)
			if err := w.writeSample(tc.sample); err == nil {
				t.Fatal("expected injected failure")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMetricsQueryScanAndIterationFailures(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		query   func(*MetricsWriter) error
	}{
		{"environment scan", []string{"only_one"}, func(w *MetricsWriter) error {
			_, err := w.QueryEnvMetrics(context.Background(), "test", 0, 1, 1)
			return err
		}},
		{"server scan", []string{"only_one"}, func(w *MetricsWriter) error {
			_, err := w.QueryServerMetrics(context.Background(), "test", "", 0, 1, 0)
			return err
		}},
		{"MQTT scan", []string{"only_one"}, func(w *MetricsWriter) error {
			_, err := w.QueryMQTTMetrics(context.Background(), "test", "", 0, 1, 0)
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, mock, closeDB := mockWriter(t)
			defer closeDB()
			mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(tc.columns).AddRow(1))
			if err := tc.query(w); err == nil {
				t.Fatal("expected scan failure")
			}
		})
	}

	w, mock, closeDB := mockWriter(t)
	defer closeDB()
	rows := sqlmock.NewRows([]string{"bucket", "server_id", "connections", "cpu", "mem", "in_msgs_rate", "out_msgs_rate", "in_bytes_rate", "out_bytes_rate", "subscriptions", "slow_consumers"}).
		AddRow(1, "s1", 1, 1, 1, 1, 1, 1, 1, 1, 1).
		RowError(0, errInjected)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	if _, err := w.QueryServerMetrics(context.Background(), "test", "", 0, 1, 1); err == nil {
		t.Fatal("expected row iteration failure")
	}
}

func TestMetricsWriterLifecycleAndCleanupErrors(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	cancel()
	<-done
	w.Run(context.Background()) // A second run is deliberately a no-op.

	w2 := NewMetricsWriter(s.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitCancel()
	if !errors.Is(w2.Wait(waitCtx), context.Canceled) {
		t.Fatal("Wait should respect cancellation before Run")
	}

	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM server_metrics WHERE rowid IN (SELECT rowid FROM server_metrics WHERE ts < ? LIMIT 10000)")).
		WillReturnError(errInjected)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM env_metrics WHERE rowid IN (SELECT rowid FROM env_metrics WHERE ts < ? LIMIT 10000)")).
		WillReturnResult(sqlmock.NewErrorResult(errInjected))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM mqtt_bridge_metrics WHERE rowid IN (SELECT rowid FROM mqtt_bridge_metrics WHERE ts < ? LIMIT 10000)")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	NewMetricsWriter(mockDB, slog.New(slog.NewTextHandler(io.Discard, nil))).deleteOld()
}

func TestStoreDirectDatabaseFailurePaths(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{db: db}

	mock.ExpectQuery("PRAGMA quick_check").WillReturnError(errInjected)
	if s.IntegrityCheck(context.Background()) == nil {
		t.Fatal("expected integrity query failure")
	}
	mock.ExpectQuery("PRAGMA quick_check").WillReturnRows(sqlmock.NewRows([]string{"quick_check"}).AddRow("corrupt"))
	if s.IntegrityCheck(context.Background()) == nil {
		t.Fatal("expected corrupt integrity result")
	}
}

func TestSchemaHelpersReportFailures(t *testing.T) {
	newTx := func(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *sql.Tx) {
		t.Helper()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		return db, mock, tx
	}

	t.Run("query", func(t *testing.T) {
		db, mock, tx := newTx(t)
		defer db.Close()
		mock.ExpectQuery("PRAGMA table_info").WillReturnError(errInjected)
		if _, err := hasColumn(tx, "users", "role"); err == nil {
			t.Fatal("expected query failure")
		}
	})
	t.Run("scan", func(t *testing.T) {
		db, mock, tx := newTx(t)
		defer db.Close()
		mock.ExpectQuery("PRAGMA table_info").WillReturnRows(sqlmock.NewRows([]string{"bad"}).AddRow(1))
		if _, err := hasColumn(tx, "users", "role"); err == nil {
			t.Fatal("expected scan failure")
		}
	})
	t.Run("alter", func(t *testing.T) {
		db, mock, tx := newTx(t)
		defer db.Close()
		mock.ExpectQuery("PRAGMA table_info").WillReturnRows(sqlmock.NewRows([]string{"cid", "name", "type", "notnull", "dflt_value", "pk"}))
		mock.ExpectExec("ALTER TABLE").WillReturnError(errInjected)
		if err := ensureColumn(tx, "users", "role", "role TEXT"); err == nil {
			t.Fatal("expected alter failure")
		}
	})
	t.Run("statement", func(t *testing.T) {
		db, mock, tx := newTx(t)
		defer db.Close()
		mock.ExpectExec("bad").WillReturnError(errInjected)
		if err := execStatements(tx, []string{"bad"}); err == nil {
			t.Fatal("expected statement failure")
		}
	})
	t.Run("legacy rebuild", func(t *testing.T) {
		db, mock, tx := newTx(t)
		defer db.Close()
		mock.ExpectExec("ALTER TABLE users").WillReturnError(errInjected)
		if err := rebuildLegacyUsers(tx); err == nil {
			t.Fatal("expected rebuild failure")
		}
	})
}

func newMigrationMock(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return &Store{db: db}, mock, func() { _ = db.Close() }
}

func expectMigrationTable(mock sqlmock.Sqlmock) {
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectMigrationCount(mock sqlmock.Sqlmock, applied int) {
	mock.ExpectQuery("SELECT COUNT(.+) FROM schema_migrations").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(applied))
}

func expectMQTTMigration(mock sqlmock.Sqlmock) {
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS mqtt_bridges").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_mqtt_bridges_env_last_seen").WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestMigrationFailureStagesRollbackAndSurfaceContext(t *testing.T) {
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{"schema table", func(m sqlmock.Sqlmock) {
			m.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").WillReturnError(errInjected)
		}},
		{"version read", func(m sqlmock.Sqlmock) {
			expectMigrationTable(m)
			m.ExpectQuery("SELECT COUNT(.+) FROM schema_migrations").WillReturnError(errInjected)
		}},
		{"begin", func(m sqlmock.Sqlmock) {
			expectMigrationTable(m)
			expectMigrationCount(m, 0)
			m.ExpectBegin().WillReturnError(errInjected)
		}},
		{"apply", func(m sqlmock.Sqlmock) {
			expectMigrationTable(m)
			expectMigrationCount(m, 1)
			expectMigrationCount(m, 0)
			m.ExpectBegin()
			m.ExpectExec("CREATE TABLE IF NOT EXISTS mqtt_bridges").WillReturnError(errInjected)
			m.ExpectRollback()
		}},
		{"record", func(m sqlmock.Sqlmock) {
			expectMigrationTable(m)
			expectMigrationCount(m, 1)
			expectMigrationCount(m, 0)
			m.ExpectBegin()
			expectMQTTMigration(m)
			m.ExpectExec("INSERT INTO schema_migrations").WillReturnError(errInjected)
			m.ExpectRollback()
		}},
		{"commit", func(m sqlmock.Sqlmock) {
			expectMigrationTable(m)
			expectMigrationCount(m, 1)
			expectMigrationCount(m, 0)
			m.ExpectBegin()
			expectMQTTMigration(m)
			m.ExpectExec("INSERT INTO schema_migrations").WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectCommit().WillReturnError(errInjected)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, closeDB := newMigrationMock(t)
			defer closeDB()
			tc.setup(mock)
			if err := s.migrate(); err == nil {
				t.Fatal("expected migration failure")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func userTableColumns() []string {
	return []string{"cid", "name", "type", "notnull", "dflt_value", "pk"}
}

func expectColumn(mock sqlmock.Sqlmock, name string, exists bool) {
	rows := sqlmock.NewRows(userTableColumns())
	if exists {
		rows.AddRow(0, name, "TEXT", 0, nil, 0)
	}
	mock.ExpectQuery("PRAGMA table_info").WillReturnRows(rows)
}

func expectLegacyColumns(mock sqlmock.Sqlmock) {
	for _, name := range []string{"role", "last_login", "failed_attempts", "last_failed_at", "must_change_password", "session_version"} {
		expectColumn(mock, name, true)
	}
}

func TestUserMigrationFailureStages(t *testing.T) {
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{"create", func(m sqlmock.Sqlmock) {
			m.ExpectExec("CREATE TABLE IF NOT EXISTS users").WillReturnError(errInjected)
		}},
		{"legacy column inspection", func(m sqlmock.Sqlmock) {
			m.ExpectExec("CREATE TABLE IF NOT EXISTS users").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery("PRAGMA table_info").WillReturnError(errInjected)
		}},
		{"provider inspection", func(m sqlmock.Sqlmock) {
			m.ExpectExec("CREATE TABLE IF NOT EXISTS users").WillReturnResult(sqlmock.NewResult(0, 0))
			expectLegacyColumns(m)
			m.ExpectQuery("PRAGMA table_info").WillReturnError(errInjected)
		}},
		{"legacy rebuild", func(m sqlmock.Sqlmock) {
			m.ExpectExec("CREATE TABLE IF NOT EXISTS users").WillReturnResult(sqlmock.NewResult(0, 0))
			expectLegacyColumns(m)
			expectColumn(m, "auth_provider", false)
			m.ExpectExec("ALTER TABLE users").WillReturnError(errInjected)
		}},
		{"local index", func(m sqlmock.Sqlmock) {
			m.ExpectExec("CREATE TABLE IF NOT EXISTS users").WillReturnResult(sqlmock.NewResult(0, 0))
			expectLegacyColumns(m)
			expectColumn(m, "auth_provider", true)
			m.ExpectExec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_local_username").WillReturnError(errInjected)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, tx := func() (*sql.DB, sqlmock.Sqlmock, *sql.Tx) {
				db, mock, err := sqlmock.New()
				if err != nil {
					t.Fatal(err)
				}
				mock.ExpectBegin()
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				return db, mock, tx
			}()
			defer db.Close()
			tc.setup(mock)
			if err := migrateUsers(tx); err == nil {
				t.Fatal("expected user migration failure")
			}
		})
	}
}

func TestUserAndTopologyFailureHandling(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateUser("admin", "old-password", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if s.ChangePassword(u.ID, "old-password", "") == nil {
		t.Fatal("empty new password should fail")
	}
	if s.ChangePassword(u.ID, "wrong-password", "new-password") == nil {
		t.Fatal("wrong old password should fail")
	}
	if s.ChangePassword(999, "old-password", "new-password") == nil {
		t.Fatal("missing user should fail")
	}
	if _, err := s.Authenticate("admin", "old-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("admin", "old-password"); err != nil {
		t.Fatal(err)
	}
	failedAt := time.Now().Add(-time.Minute)
	if _, err := s.db.Exec(`UPDATE users SET last_failed_at=? WHERE id=?`, failedAt, u.ID); err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUsers()
	if err != nil || len(users) != 1 || users[0].LastLogin == nil || users[0].LastFailedAt == nil {
		t.Fatalf("users=%+v err=%v", users, err)
	}

	// An external identity with recorded failure metadata exercises nullable
	// timestamp restoration during an update.
	external, err := s.UpsertExternalUser("ldap", "subject", "person", "Person", "p@example.com", RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE users SET last_failed_at=? WHERE id=?`, failedAt, external.ID); err != nil {
		t.Fatal(err)
	}
	external, err = s.UpsertExternalUser("ldap", "subject", "person", "Person", "p@example.com", RoleViewer)
	if err != nil || external.LastFailedAt == nil {
		t.Fatalf("external=%+v err=%v", external, err)
	}
}

func TestSaveTopologyPositionsFailureStages(t *testing.T) {
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
		rows  []NodePosition
	}{
		{"begin", func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(errInjected) }, nil},
		{"delete", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM topology_positions").WillReturnError(errInjected)
			m.ExpectRollback()
		}, nil},
		{"prepare", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM topology_positions").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectPrepare("INSERT INTO topology_positions").WillReturnError(errInjected)
			m.ExpectRollback()
		}, nil},
		{"insert", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM topology_positions").WillReturnResult(sqlmock.NewResult(0, 0))
			p := m.ExpectPrepare("INSERT INTO topology_positions")
			p.ExpectExec().WillReturnError(errInjected)
			m.ExpectRollback()
		}, []NodePosition{{NodeID: "n1"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tc.setup(mock)
			if err := (&Store{db: db}).SaveTopologyPositions("test", tc.rows); err == nil {
				t.Fatal("expected injected topology failure")
			}
		})
	}
}
