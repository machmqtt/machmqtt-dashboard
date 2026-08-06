package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	RoleAdmin     = "admin"
	RoleViewer    = "viewer"
	ProviderLocal = "local"
)

type User struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	Role               string     `json:"role"`
	AuthProvider       string     `json:"auth_provider"`
	ExternalSubject    string     `json:"external_subject,omitempty"`
	DisplayName        string     `json:"display_name,omitempty"`
	Email              string     `json:"email,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLogin          *time.Time `json:"last_login,omitempty"`
	FailedAttempts     int        `json:"failed_attempts"`
	LastFailedAt       *time.Time `json:"last_failed_at,omitempty"`
	MustChangePassword bool       `json:"must_change_password"`
	// SessionVersion is embedded in issued JWTs; bumping it invalidates every
	// outstanding session for this user (logout, password change, deletion).
	// Never exposed to the API.
	SessionVersion int64 `json:"-"`
}

// MinPasswordLength is the minimum accepted password length for new users and
// password changes.
const MinPasswordLength = 8

// Account-lockout policy: after this many consecutive failed logins within the
// window, further attempts are rejected until the window elapses. This is a
// per-account throttle complementing the per-IP login rate limiter.
const (
	maxFailedAttempts = 10
	lockoutWindow     = 15 * time.Minute
)

// ErrPasswordTooShort is returned when a password is below MinPasswordLength.
var ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)

// ErrAccountLocked is returned when an account is temporarily locked out after
// too many failed logins.
var ErrAccountLocked = fmt.Errorf("account temporarily locked due to too many failed login attempts")

// dummyBcryptHash is compared against on the user-not-found path so that login
// timing does not reveal whether a username exists (bcrypt is deliberately slow).
var dummyBcryptHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("timing-equalization-placeholder"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}()

type Store struct {
	db       *sql.DB
	dbPath   string
	lockFile *os.File
}

func Open(dataDir string) (*Store, error) {
	// The dir holds the SQLite database, which stores credential material
	// (password hashes, per-cluster admin API tokens), so it must not be readable
	// by other local users. MkdirAll leaves the mode of an already-existing dir
	// untouched, so 0700 is re-asserted on every open rather than only at create
	// time; a dir we cannot secure is treated as fatal.
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure data dir: %w", err)
	}
	lockFile, err := acquireInstanceLock(filepath.Join(dataDir, "dashboard.lock"))
	if err != nil {
		return nil, fmt.Errorf("acquire instance lock (another dashboard may be using %s): %w", dataDir, err)
	}
	releaseLock := func() { _ = releaseInstanceLock(lockFile) }

	dbPath := filepath.Join(dataDir, "dashboard.db")
	// synchronous(normal) is the recommended durability level with WAL: it avoids
	// an fsync on every commit (safe against app crashes; only a power loss can
	// lose the last few transactions, acceptable for monitoring time-series).
	dsn := "file:" + dbPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=synchronous(normal)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		releaseLock()
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Cap connections so a burst of concurrent queries can't fan out unboundedly
	// against the single SQLite file; metrics writes are serialized through one
	// goroutine, and other writers are low-frequency.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		releaseLock()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Enable incremental auto_vacuum before creating tables so retention deletes
	// can later return freed pages to the OS (via PRAGMA incremental_vacuum).
	if err := ensureIncrementalAutoVacuum(db); err != nil {
		_ = db.Close()
		releaseLock()
		return nil, fmt.Errorf("configure auto_vacuum: %w", err)
	}

	s := &Store{db: db, dbPath: dbPath, lockFile: lockFile}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		releaseLock()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// ensureIncrementalAutoVacuum sets the database to incremental auto_vacuum mode.
// The mode is a property of the database file: on a new (empty) database the
// PRAGMA takes effect immediately, and on a database created before this setting
// (auto_vacuum=NONE) a one-time VACUUM rewrites the file in the new format. Both
// statements run on a single pinned connection because the pending mode change
// only applies to the VACUUM issued on the same connection.
func ensureIncrementalAutoVacuum(db *sql.DB) error {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()

	var mode int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return err
	}
	if mode == 2 { // 2 = INCREMENTAL, already configured
		return nil
	}
	if _, err := conn.ExecContext(context.Background(), "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return err
	}
	// VACUUM applies the mode change (and, on an existing DB, rewrites it).
	if _, err := conn.ExecContext(context.Background(), "VACUUM"); err != nil {
		return err
	}
	return nil
}

func (s *Store) Close() error {
	dbErr := s.db.Close()
	if s.lockFile == nil {
		return dbErr
	}
	lockErr := releaseInstanceLock(s.lockFile)
	s.lockFile = nil
	return errors.Join(dbErr, lockErr)
}

// DB returns the underlying database handle for MetricsWriter and health checks.
func (s *Store) DB() *sql.DB { return s.db }

// WALSize returns the current write-ahead log size. A missing WAL is a valid
// zero-size state before the first write or after a checkpoint.
func (s *Store) WALSize() (int64, error) {
	info, err := os.Stat(s.dbPath + "-wal")
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect database WAL: %w", err)
	}
	return info.Size(), nil
}

func (s *Store) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("database integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("database integrity check failed: %s", result)
	}
	return nil
}

// Backup creates a transactionally consistent SQLite snapshot using VACUUM
// INTO. The destination must not already exist.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if destination == "" {
		return fmt.Errorf("backup destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, destination); err != nil {
		return fmt.Errorf("create database backup: %w", err)
	}
	return nil
}

// Ping verifies the database connection is usable. Used by the /healthz probe.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) migrate() error {
	if err := s.migrateVersioned(); err != nil {
		return err
	}
	if err := migrateUsersSchema(s.db); err != nil {
		return err
	}

	// Cluster configuration (persisted, managed via admin UI).
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS clusters (
			id             TEXT PRIMARY KEY,
			name           TEXT NOT NULL,
			servers        TEXT NOT NULL DEFAULT '[]',
			mqtt_bridges   TEXT NOT NULL DEFAULT '[]',
			mqtt_discovery TEXT,
			tls            TEXT,
			admin_token    TEXT NOT NULL DEFAULT '',
			nats_conn      TEXT,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	// Migration: add nats_conn for existing databases (silently ignored if already present).
	s.db.Exec(`ALTER TABLE clusters ADD COLUMN nats_conn TEXT`)

	// MQTT bridge discovery persistence.
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS mqtt_bridges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			env TEXT NOT NULL,
			ip TEXT NOT NULL,
			server_id TEXT NOT NULL,
			admin_url TEXT NOT NULL DEFAULT '',
			last_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(env, ip, server_id)
		)
	`)
	if err != nil {
		return err
	}

	// Time-series metric tables.
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS server_metrics (
			ts INTEGER NOT NULL,
			env TEXT NOT NULL,
			server_id TEXT NOT NULL,
			connections INTEGER,
			in_msgs INTEGER,
			out_msgs INTEGER,
			in_bytes INTEGER,
			out_bytes INTEGER,
			cpu REAL,
			mem INTEGER,
			subscriptions INTEGER,
			slow_consumers INTEGER,
			routes INTEGER,
			leafnodes INTEGER,
			in_msgs_rate REAL,
			out_msgs_rate REAL,
			in_bytes_rate REAL,
			out_bytes_rate REAL,
			healthy INTEGER
		)
	`)
	if err != nil {
		return err
	}
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_server_metrics_env_sid_ts ON server_metrics (env, server_id, ts)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_server_metrics_ts ON server_metrics (ts)`)

	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS env_metrics (
			ts INTEGER NOT NULL,
			env TEXT NOT NULL,
			server_count INTEGER,
			healthy_count INTEGER,
			connection_count INTEGER,
			in_msgs_rate REAL,
			out_msgs_rate REAL,
			in_bytes_rate REAL,
			out_bytes_rate REAL,
			subscriptions INTEGER
		)
	`)
	if err != nil {
		return err
	}
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_env_metrics_env_ts ON env_metrics (env, ts)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_env_metrics_ts ON env_metrics (ts)`)

	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS mqtt_bridge_metrics (
			ts INTEGER NOT NULL,
			env TEXT NOT NULL,
			bridge_id TEXT NOT NULL,
			connections_active INTEGER,
			in_msgs_rate REAL,
			out_msgs_rate REAL,
			in_bytes_rate REAL,
			out_bytes_rate REAL,
			msgs_recv_qos0 INTEGER,
			msgs_recv_qos1 INTEGER,
			msgs_sent_qos0 INTEGER,
			msgs_sent_qos1 INTEGER,
			msgs_recv_qos2 INTEGER,
			msgs_sent_qos2 INTEGER,
			session_write_behind_depth INTEGER,
			consumer_pending_messages INTEGER,
			stalled_consumers INTEGER,
			sockets_open INTEGER,
			inflight_out_messages INTEGER,
			op_queue_depth INTEGER,
			op_suspended_conns INTEGER,
			worker_pool_queue_depth INTEGER,
			pool_slot_connected INTEGER,
			retained_messages INTEGER,
			subscriptions_active INTEGER,
			go_heap_inuse_bytes INTEGER,
			go_goroutines INTEGER,
			scram_sessions_active INTEGER
		)
	`)
	if err != nil {
		return err
	}
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_mqtt_bridge_metrics_env_bid_ts ON mqtt_bridge_metrics (env, bridge_id, ts)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_mqtt_bridge_metrics_ts ON mqtt_bridge_metrics (ts)`)
	// idx_mqtt_bridge_metrics_env_ts covers the all-bridges aggregate query
	// (no bridge_id predicate) that scans the full env over a time range.
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_mqtt_bridge_metrics_env_ts ON mqtt_bridge_metrics (env, ts)`)
	// Migrations for existing databases.
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN msgs_recv_qos2 INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN msgs_sent_qos2 INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN session_write_behind_depth INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN consumer_pending_messages INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN stalled_consumers INTEGER`)
	// Trend-line gauges (added with the machmqtt observability sync). Existing
	// rows get NULL; QueryMQTTMetrics scans these as NullFloat64 so pre-migration
	// buckets render as gaps rather than a spurious zero.
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN sockets_open INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN inflight_out_messages INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN op_queue_depth INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN op_suspended_conns INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN worker_pool_queue_depth INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN pool_slot_connected INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN retained_messages INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN subscriptions_active INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN go_heap_inuse_bytes INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN go_goroutines INTEGER`)
	s.db.Exec(`ALTER TABLE mqtt_bridge_metrics ADD COLUMN scram_sessions_active INTEGER`)

	// Topology node position persistence.
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS topology_positions (
			env TEXT NOT NULL,
			node_id TEXT NOT NULL,
			x REAL NOT NULL,
			y REAL NOT NULL,
			PRIMARY KEY (env, node_id)
		)
	`)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS topology_camera (
			env TEXT NOT NULL PRIMARY KEY,
			zoom REAL NOT NULL,
			center_x REAL NOT NULL,
			center_y REAL NOT NULL
		)
	`)
	if err != nil {
		return err
	}

	return nil
}

func (s *Store) migrateVersioned() error {
	type migration struct {
		version int
		name    string
		apply   func(*sql.Tx) error
	}
	migrations := []migration{
		{1, "users and external identities", migrateUsers},
		{2, "mqtt bridge discovery", migrateMQTTBridges},
		{3, "time-series metrics", migrateMetrics},
		{4, "topology persistence", migrateTopology},
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema migration table: %w", err)
	}
	for _, migration := range migrations {
		var applied int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, migration.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("read schema migration %d: %w", migration.version, err)
		}
		if applied != 0 {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin schema migration %d: %w", migration.version, err)
		}
		if err := migration.apply(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
			migration.version, migration.name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record schema migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema migration %d: %w", migration.version, err)
		}
	}
	return nil
}

// migrateUsersSchema upgrades the original local-only table without losing
// users. The rebuild removes the old global username uniqueness constraint:
// local usernames remain unique, while external identities are keyed by the
// provider and immutable subject so an LDAP/OIDC user may also be named admin.
func migrateUsersSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := migrateUsers(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateUsers(tx *sql.Tx) error {
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			password_hash TEXT,
			role TEXT NOT NULL DEFAULT 'viewer',
			auth_provider TEXT NOT NULL DEFAULT 'local',
			external_subject TEXT,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login DATETIME,
			failed_attempts INTEGER NOT NULL DEFAULT 0,
			last_failed_at DATETIME,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			token_version INTEGER NOT NULL DEFAULT 0,
			UNIQUE(auth_provider, external_subject)
		)
	`); err != nil {
		return err
	}

	legacyColumns := []struct{ name, definition string }{
		{"role", "role TEXT NOT NULL DEFAULT 'viewer'"},
		{"last_login", "last_login DATETIME"},
		{"failed_attempts", "failed_attempts INTEGER NOT NULL DEFAULT 0"},
		{"last_failed_at", "last_failed_at DATETIME"},
		{"must_change_password", "must_change_password INTEGER NOT NULL DEFAULT 0"},
		{"token_version", "token_version INTEGER NOT NULL DEFAULT 0"},
	}
	for _, column := range legacyColumns {
		if err := ensureColumn(tx, "users", column.name, column.definition); err != nil {
			return err
		}
	}

	hasProvider, err := hasColumn(tx, "users", "auth_provider")
	if err != nil {
		return err
	}
	if !hasProvider {
		if err := rebuildLocalUsers(tx); err != nil {
			return err
		}
	} else {
		identityColumns := []struct{ name, definition string }{
			{"external_subject", "external_subject TEXT"},
			{"display_name", "display_name TEXT NOT NULL DEFAULT ''"},
			{"email", "email TEXT NOT NULL DEFAULT ''"},
		}
		for _, column := range identityColumns {
			if err := ensureColumn(tx, "users", column.name, column.definition); err != nil {
				return err
			}
		}
		// Early enterprise-auth builds called this column session_version.
		// Preserve its revocation values while standardizing on token_version.
		hasSessionVersion, err := hasColumn(tx, "users", "session_version")
		if err != nil {
			return err
		}
		if hasSessionVersion {
			if _, err := tx.Exec(`UPDATE users SET token_version = MAX(token_version, session_version)`); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_local_username ON users(username) WHERE auth_provider = 'local'`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_external_identity ON users(auth_provider, external_subject) WHERE external_subject IS NOT NULL`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_users_local_admin ON users(auth_provider, role)`); err != nil {
		return err
	}
	return nil
}

func migrateMQTTBridges(tx *sql.Tx) error {
	return execStatements(tx, []string{`
		CREATE TABLE IF NOT EXISTS mqtt_bridges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			env TEXT NOT NULL,
			ip TEXT NOT NULL,
			server_id TEXT NOT NULL,
			admin_url TEXT NOT NULL DEFAULT '',
			last_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(env, ip, server_id)
		)
	`, `CREATE INDEX IF NOT EXISTS idx_mqtt_bridges_env_last_seen ON mqtt_bridges (env, last_seen)`})
}

func migrateMetrics(tx *sql.Tx) error {
	return execStatements(tx, []string{`
		CREATE TABLE IF NOT EXISTS server_metrics (
			ts INTEGER NOT NULL, env TEXT NOT NULL, server_id TEXT NOT NULL,
			connections INTEGER, in_msgs INTEGER, out_msgs INTEGER,
			in_bytes INTEGER, out_bytes INTEGER, cpu REAL, mem INTEGER,
			subscriptions INTEGER, slow_consumers INTEGER, routes INTEGER,
			leafnodes INTEGER, in_msgs_rate REAL, out_msgs_rate REAL,
			in_bytes_rate REAL, out_bytes_rate REAL, healthy INTEGER
		)
	`, `CREATE INDEX IF NOT EXISTS idx_server_metrics_env_sid_ts ON server_metrics (env, server_id, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_server_metrics_env_ts ON server_metrics (env, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_server_metrics_ts ON server_metrics (ts)`, `
		CREATE TABLE IF NOT EXISTS env_metrics (
			ts INTEGER NOT NULL, env TEXT NOT NULL, server_count INTEGER,
			healthy_count INTEGER, connection_count INTEGER, in_msgs_rate REAL,
			out_msgs_rate REAL, in_bytes_rate REAL, out_bytes_rate REAL,
			subscriptions INTEGER
		)
	`, `CREATE INDEX IF NOT EXISTS idx_env_metrics_env_ts ON env_metrics (env, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_env_metrics_ts ON env_metrics (ts)`, `
		CREATE TABLE IF NOT EXISTS mqtt_bridge_metrics (
			ts INTEGER NOT NULL, env TEXT NOT NULL, bridge_id TEXT NOT NULL,
			connections_active INTEGER, in_msgs_rate REAL, out_msgs_rate REAL,
			in_bytes_rate REAL, out_bytes_rate REAL, msgs_recv_qos0 INTEGER,
			msgs_recv_qos1 INTEGER, msgs_sent_qos0 INTEGER, msgs_sent_qos1 INTEGER
		)
	`, `CREATE INDEX IF NOT EXISTS idx_mqtt_bridge_metrics_env_bid_ts ON mqtt_bridge_metrics (env, bridge_id, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_mqtt_bridge_metrics_env_ts ON mqtt_bridge_metrics (env, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_mqtt_bridge_metrics_ts ON mqtt_bridge_metrics (ts)`})
}

func migrateTopology(tx *sql.Tx) error {
	return execStatements(tx, []string{`
		CREATE TABLE IF NOT EXISTS topology_positions (
			env TEXT NOT NULL, node_id TEXT NOT NULL, x REAL NOT NULL,
			y REAL NOT NULL, PRIMARY KEY (env, node_id)
		)
	`, `
		CREATE TABLE IF NOT EXISTS topology_camera (
			env TEXT NOT NULL PRIMARY KEY, zoom REAL NOT NULL,
			center_x REAL NOT NULL, center_y REAL NOT NULL
		)
	`})
}

func execStatements(tx *sql.Tx, statements []string) error {
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func hasColumn(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func ensureColumn(tx *sql.Tx, table, column, definition string) error {
	exists, err := hasColumn(tx, table, column)
	if err != nil || exists {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func rebuildLocalUsers(tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE users RENAME TO users_local_legacy`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			password_hash TEXT,
			role TEXT NOT NULL DEFAULT 'viewer',
			auth_provider TEXT NOT NULL DEFAULT 'local',
			external_subject TEXT,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login DATETIME,
			failed_attempts INTEGER NOT NULL DEFAULT 0,
			last_failed_at DATETIME,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			token_version INTEGER NOT NULL DEFAULT 0,
			UNIQUE(auth_provider, external_subject)
		)`,
		`INSERT INTO users (
			id, username, password_hash, role, auth_provider, created_at,
			last_login, failed_attempts, last_failed_at, must_change_password,
			token_version
		) SELECT id, username, password_hash, role, 'local', created_at,
			last_login, failed_attempts, last_failed_at, must_change_password,
			token_version FROM users_local_legacy`,
		`DROP TABLE users_local_legacy`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migrate local users: %w", err)
		}
	}
	return nil
}

// rebuildLegacyUsers is retained as the migration-level name used by audit
// tests and older extensions.
func rebuildLegacyUsers(tx *sql.Tx) error { return rebuildLocalUsers(tx) }

func (s *Store) UserCount() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (s *Store) CreateUser(username, password, role string) (*User, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password are required")
	}
	if role != RoleAdmin && role != RoleViewer {
		return nil, fmt.Errorf("invalid role: %q", role)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	result, err := s.db.Exec(
		"INSERT INTO users (username, password_hash, role, auth_provider) VALUES (?, ?, ?, ?)",
		username, string(hash), role, ProviderLocal,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	id, _ := result.LastInsertId()
	return &User{ID: id, Username: username, Role: role, AuthProvider: ProviderLocal, CreatedAt: time.Now()}, nil
}

func (s *Store) Authenticate(username, password string) (*User, error) {
	var u User
	var hash string
	var lastLogin, lastFailed sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, username, password_hash, role, auth_provider,
			COALESCE(external_subject, ''), display_name, email, created_at,
			last_login, failed_attempts, last_failed_at, must_change_password,
			token_version
		 FROM users WHERE auth_provider = ? AND username = ?`,
		ProviderLocal, username,
	).Scan(
		&u.ID, &u.Username, &hash, &u.Role, &u.AuthProvider,
		&u.ExternalSubject, &u.DisplayName, &u.Email, &u.CreatedAt,
		&lastLogin, &u.FailedAttempts, &lastFailed, &u.MustChangePassword,
		&u.SessionVersion,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			// Compare against a dummy hash so the not-found path costs the same
			// as a wrong-password path, preventing username enumeration by timing.
			bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
			return nil, fmt.Errorf("invalid credentials")
		}
		return nil, err
	}

	// Per-account lockout: too many recent consecutive failures blocks further
	// attempts until the window elapses, independent of the per-IP limiter.
	if u.FailedAttempts >= maxFailedAttempts && lastFailed.Valid && time.Since(lastFailed.Time) < lockoutWindow {
		return nil, ErrAccountLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		// Record failed attempt.
		now := time.Now()
		if _, err := s.db.Exec("UPDATE users SET failed_attempts = failed_attempts + 1, last_failed_at = ? WHERE id = ?", now, u.ID); err != nil {
			slog.Warn("failed to record login attempt", "user", u.Username, "err", err)
		}
		return nil, fmt.Errorf("invalid credentials")
	}

	// Successful login: update last_login to now, reset failed attempts.
	now := time.Now()
	if _, err := s.db.Exec("UPDATE users SET last_login = ?, failed_attempts = 0 WHERE id = ?", now, u.ID); err != nil {
		slog.Warn("failed to update login timestamp", "user", u.Username, "err", err)
	}
	u.FailedAttempts = 0
	// Return the PREVIOUS last_login (the value before this login) so the UI can
	// show "last seen at …"; on the very first login there is none.
	if lastLogin.Valid {
		prev := lastLogin.Time
		u.LastLogin = &prev
	}

	return &u, nil
}

// UpsertExternalUser links an external identity by provider plus immutable
// subject. Usernames and email addresses are mutable attributes, not account
// keys, and may safely collide with local usernames.
func (s *Store) UpsertExternalUser(provider, subject, username, displayName, email, role string) (*User, error) {
	if provider == "" || provider == ProviderLocal {
		return nil, fmt.Errorf("invalid external provider")
	}
	if subject == "" || username == "" {
		return nil, fmt.Errorf("external subject and username are required")
	}
	if role != RoleAdmin && role != RoleViewer {
		return nil, fmt.Errorf("invalid role: %q", role)
	}

	now := time.Now()
	_, err := s.db.Exec(`
		INSERT INTO users (
			username, password_hash, role, auth_provider, external_subject,
			display_name, email, last_login
		) VALUES (?, NULL, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(auth_provider, external_subject) DO UPDATE SET
			username = excluded.username,
			role = excluded.role,
			display_name = excluded.display_name,
			email = excluded.email,
			last_login = excluded.last_login,
			failed_attempts = 0,
			token_version = users.token_version + CASE WHEN users.role <> excluded.role THEN 1 ELSE 0 END
	`, username, role, provider, subject, displayName, email, now)
	if err != nil {
		return nil, fmt.Errorf("upsert external user: %w", err)
	}

	var u User
	var lastLogin, lastFailed sql.NullTime
	err = s.db.QueryRow(`
		SELECT id, username, role, auth_provider, external_subject,
			display_name, email, created_at, last_login, failed_attempts,
			last_failed_at, must_change_password, token_version
		FROM users WHERE auth_provider = ? AND external_subject = ?
	`, provider, subject).Scan(
		&u.ID, &u.Username, &u.Role, &u.AuthProvider, &u.ExternalSubject,
		&u.DisplayName, &u.Email, &u.CreatedAt, &lastLogin,
		&u.FailedAttempts, &lastFailed, &u.MustChangePassword,
		&u.SessionVersion,
	)
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.Time
	}
	if lastFailed.Valid {
		u.LastFailedAt = &lastFailed.Time
	}
	return &u, nil
}

func (s *Store) ChangePassword(userID int64, oldPassword, newPassword string) error {
	if newPassword == "" {
		return fmt.Errorf("new password is required")
	}
	var hash string
	err := s.db.QueryRow(
		"SELECT password_hash FROM users WHERE id = ? AND auth_provider = ?",
		userID, ProviderLocal,
	).Scan(&hash)
	if err != nil {
		return fmt.Errorf("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPassword)); err != nil {
		return fmt.Errorf("invalid old password")
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// Bump token_version so any other outstanding sessions for this user are
	// invalidated once the password changes.
	_, err = s.db.Exec(
		"UPDATE users SET password_hash = ?, must_change_password = 0, token_version = token_version + 1 WHERE id = ?",
		string(newHash), userID,
	)
	return err
}

// BumpTokenVersion invalidates all outstanding sessions for a user (used on
// logout, which signs the user out everywhere).
func (s *Store) BumpTokenVersion(userID int64) error {
	_, err := s.db.Exec("UPDATE users SET token_version = token_version + 1 WHERE id = ?", userID)
	return err
}

// SessionState is the per-request authorization state re-read from the database
// so that a stale JWT (revoked, role-changed, forced-password-change) is caught.
type SessionState struct {
	Role               string
	SessionVersion     int64
	MustChangePassword bool
}

// GetSessionState returns the current authorization state for a user, or
// sql.ErrNoRows if the user no longer exists.
func (s *Store) GetSessionState(userID int64) (SessionState, error) {
	var st SessionState
	err := s.db.QueryRow(
		"SELECT role, token_version, must_change_password FROM users WHERE id = ?", userID,
	).Scan(&st.Role, &st.SessionVersion, &st.MustChangePassword)
	return st, err
}

func (s *Store) GetUser(id int64) (*User, error) {
	var u User
	var lastLogin, lastFailed sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, username, role, auth_provider, COALESCE(external_subject, ''),
			display_name, email, created_at, last_login, failed_attempts,
			last_failed_at, must_change_password, token_version
		 FROM users WHERE id = ?`, id,
	).Scan(
		&u.ID, &u.Username, &u.Role, &u.AuthProvider, &u.ExternalSubject,
		&u.DisplayName, &u.Email, &u.CreatedAt, &lastLogin,
		&u.FailedAttempts, &lastFailed, &u.MustChangePassword,
		&u.SessionVersion,
	)
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.Time
	}
	if lastFailed.Valid {
		u.LastFailedAt = &lastFailed.Time
	}
	return &u, nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`
		SELECT id, username, role, auth_provider, COALESCE(external_subject, ''),
			display_name, email, created_at, last_login, failed_attempts,
			last_failed_at, must_change_password, token_version
		FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []User
	for rows.Next() {
		var u User
		var lastLogin, lastFailed sql.NullTime
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Role, &u.AuthProvider,
			&u.ExternalSubject, &u.DisplayName, &u.Email, &u.CreatedAt,
			&lastLogin, &u.FailedAttempts, &lastFailed,
			&u.MustChangePassword, &u.SessionVersion,
		); err != nil {
			return nil, err
		}
		if lastLogin.Valid {
			u.LastLogin = &lastLogin.Time
		}
		if lastFailed.Valid {
			u.LastFailedAt = &lastFailed.Time
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) DeleteUser(id int64) error {
	// Keep the final-local-admin check and deletion in one SQLite statement so
	// concurrent admin requests cannot both pass a separate count check.
	result, err := s.db.Exec(`
		DELETE FROM users
		WHERE id = ?
		  AND NOT (
			auth_provider = ? AND role = ? AND
			(SELECT COUNT(*) FROM users WHERE auth_provider = ? AND role = ?) <= 1
		  )
	`, id, ProviderLocal, RoleAdmin, ProviderLocal, RoleAdmin)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		var exists int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", id).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return fmt.Errorf("cannot delete the last local administrator")
		}
		return fmt.Errorf("user not found")
	}
	return nil
}

// EnsureBreakGlassAdmin guarantees at least one local administrator. It never
// installs a known default password; a fresh deployment must provide an
// explicit bootstrap secret and rotate it on first login.
func (s *Store) EnsureBreakGlassAdmin(bootstrapPassword string) (*User, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM users WHERE auth_provider = ? AND role = ?",
		ProviderLocal, RoleAdmin,
	).Scan(&count)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, nil
	}
	if len(bootstrapPassword) < 12 {
		return nil, fmt.Errorf("a bootstrap password of at least 12 characters is required when no local administrator exists")
	}

	var existingAdminID int64
	err = s.db.QueryRow(
		"SELECT id FROM users WHERE auth_provider = ? AND username = ?",
		ProviderLocal, "admin",
	).Scan(&existingAdminID)
	if err == nil {
		if _, err := s.db.Exec(
			"UPDATE users SET role = ?, must_change_password = 1, token_version = token_version + 1 WHERE id = ?",
			RoleAdmin, existingAdminID,
		); err != nil {
			return nil, err
		}
		return s.GetUser(existingAdminID)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	u, err := s.CreateUser("admin", bootstrapPassword, RoleAdmin)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", u.ID); err != nil {
		return nil, err
	}
	u.MustChangePassword = true
	return u, nil
}

// MQTTBridgeRecord is a persisted discovered bridge.
type MQTTBridgeRecord struct {
	Env      string    `json:"env"`
	IP       string    `json:"ip"`
	ServerID string    `json:"server_id"`
	AdminURL string    `json:"admin_url"`
	LastSeen time.Time `json:"last_seen"`
}

func (s *Store) UpsertMQTTBridge(env, ip, serverID, adminURL string) error {
	_, err := s.db.Exec(`
		INSERT INTO mqtt_bridges (env, ip, server_id, admin_url, last_seen)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(env, ip, server_id) DO UPDATE SET
			admin_url = excluded.admin_url,
			last_seen = CURRENT_TIMESTAMP
	`, env, ip, serverID, adminURL)
	return err
}

func (s *Store) ListMQTTBridges(env string) ([]MQTTBridgeRecord, error) {
	rows, err := s.db.Query(
		"SELECT env, ip, server_id, admin_url, last_seen FROM mqtt_bridges WHERE env = ? ORDER BY ip",
		env,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MQTTBridgeRecord
	for rows.Next() {
		var r MQTTBridgeRecord
		if err := rows.Scan(&r.Env, &r.IP, &r.ServerID, &r.AdminURL, &r.LastSeen); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *Store) DeleteStaleMQTTBridges(env string, olderThan time.Duration) error {
	_, err := s.db.Exec(
		"DELETE FROM mqtt_bridges WHERE env = ? AND last_seen < ?",
		env, time.Now().Add(-olderThan),
	)
	return err
}

// NodePosition is a persisted topology node position.
type NodePosition struct {
	NodeID string  `json:"node_id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

func (s *Store) GetTopologyPositions(env string) ([]NodePosition, error) {
	rows, err := s.db.Query(
		"SELECT node_id, x, y FROM topology_positions WHERE env = ?", env,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var positions []NodePosition
	for rows.Next() {
		var p NodePosition
		if err := rows.Scan(&p.NodeID, &p.X, &p.Y); err != nil {
			return nil, err
		}
		positions = append(positions, p)
	}
	return positions, rows.Err()
}

// CameraState is a persisted topology camera (zoom + pan).
type CameraState struct {
	Zoom    float64 `json:"zoom"`
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`
}

func (s *Store) GetTopologyCamera(env string) (*CameraState, error) {
	var c CameraState
	err := s.db.QueryRow(
		"SELECT zoom, center_x, center_y FROM topology_camera WHERE env = ?", env,
	).Scan(&c.Zoom, &c.CenterX, &c.CenterY)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) SaveTopologyCamera(env string, c CameraState) error {
	_, err := s.db.Exec(`
		INSERT INTO topology_camera (env, zoom, center_x, center_y)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(env) DO UPDATE SET zoom = excluded.zoom, center_x = excluded.center_x, center_y = excluded.center_y
	`, env, c.Zoom, c.CenterX, c.CenterY)
	return err
}

func (s *Store) DeleteTopologyCamera(env string) error {
	_, err := s.db.Exec("DELETE FROM topology_camera WHERE env = ?", env)
	return err
}

func (s *Store) SaveTopologyPositions(env string, positions []NodePosition) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM topology_positions WHERE env = ?", env); err != nil {
		return err
	}

	stmt, err := tx.Prepare(
		"INSERT INTO topology_positions (env, node_id, x, y) VALUES (?, ?, ?, ?)",
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range positions {
		if _, err := stmt.Exec(env, p.NodeID, p.X, p.Y); err != nil {
			return err
		}
	}

	return tx.Commit()
}
