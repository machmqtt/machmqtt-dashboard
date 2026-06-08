package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

type User struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	Role               string     `json:"role"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLogin          *time.Time `json:"last_login,omitempty"`
	FailedAttempts     int        `json:"failed_attempts"`
	LastFailedAt       *time.Time `json:"last_failed_at,omitempty"`
	MustChangePassword bool       `json:"must_change_password"`
}

type Store struct {
	db *sql.DB
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "dashboard.db")
	dsn := "file:" + dbPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying database handle for use by MetricsWriter.
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'viewer',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login DATETIME,
			failed_attempts INTEGER NOT NULL DEFAULT 0,
			last_failed_at DATETIME,
			must_change_password INTEGER NOT NULL DEFAULT 0
		)
	`)
	if err != nil {
		return err
	}

	// Migration columns for upgrades from older schemas.
	s.db.Exec(`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'viewer'`)
	s.db.Exec(`ALTER TABLE users ADD COLUMN last_login DATETIME`)
	s.db.Exec(`ALTER TABLE users ADD COLUMN failed_attempts INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE users ADD COLUMN last_failed_at DATETIME`)
	s.db.Exec(`ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0`)

	// Cluster configuration (persisted, managed via admin UI).
	_, err = s.db.Exec(`
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
			stalled_consumers INTEGER
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

func (s *Store) UserCount() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (s *Store) CreateUser(username, password, role string) (*User, error) {
	if role != RoleAdmin && role != RoleViewer {
		return nil, fmt.Errorf("invalid role: %q", role)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	result, err := s.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)",
		username, string(hash), role,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	id, _ := result.LastInsertId()
	return &User{ID: id, Username: username, Role: role, CreatedAt: time.Now()}, nil
}

func (s *Store) Authenticate(username, password string) (*User, error) {
	var u User
	var hash string
	var lastLogin, lastFailed sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, created_at, last_login, failed_attempts, last_failed_at, must_change_password FROM users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.CreatedAt, &lastLogin, &u.FailedAttempts, &lastFailed, &u.MustChangePassword)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("invalid credentials")
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		// Record failed attempt.
		now := time.Now()
		if _, err := s.db.Exec("UPDATE users SET failed_attempts = failed_attempts + 1, last_failed_at = ? WHERE id = ?", now, u.ID); err != nil {
			slog.Warn("failed to record login attempt", "user", u.Username, "err", err)
		}
		return nil, fmt.Errorf("invalid credentials")
	}

	// Successful login: update last_login, reset failed attempts.
	now := time.Now()
	if _, err := s.db.Exec("UPDATE users SET last_login = ?, failed_attempts = 0 WHERE id = ?", now, u.ID); err != nil {
		slog.Warn("failed to update login timestamp", "user", u.Username, "err", err)
	}
	u.LastLogin = &now
	u.FailedAttempts = 0
	if lastLogin.Valid {
		// Return the previous last_login for display (the one before this login).
		u.LastLogin = &now
	}

	return &u, nil
}

func (s *Store) ChangePassword(userID int64, oldPassword, newPassword string) error {
	var hash string
	err := s.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&hash)
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

	_, err = s.db.Exec("UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?", string(newHash), userID)
	return err
}

func (s *Store) GetUser(id int64) (*User, error) {
	var u User
	var lastLogin, lastFailed sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, role, created_at, last_login, failed_attempts, last_failed_at, must_change_password FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &lastLogin, &u.FailedAttempts, &lastFailed, &u.MustChangePassword)
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
	rows, err := s.db.Query("SELECT id, username, role, created_at, last_login, failed_attempts, last_failed_at, must_change_password FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var lastLogin, lastFailed sql.NullTime
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &lastLogin, &u.FailedAttempts, &lastFailed, &u.MustChangePassword); err != nil {
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
	// Prevent deleting the default admin account (id=1, username=admin).
	var username string
	err := s.db.QueryRow("SELECT username FROM users WHERE id = ?", id).Scan(&username)
	if err != nil {
		return fmt.Errorf("user not found")
	}
	if id == 1 && username == "admin" {
		return fmt.Errorf("cannot delete the default admin account")
	}

	result, err := s.db.Exec("DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// EnsureDefaultAdmin creates the admin/admin user if no users exist.
// The default user is flagged with must_change_password so the UI
// forces a password change on first login.
func (s *Store) EnsureDefaultAdmin() (*User, error) {
	count, err := s.UserCount()
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, nil
	}
	u, err := s.CreateUser("admin", "admin", RoleAdmin)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", u.ID)
	if err != nil {
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
