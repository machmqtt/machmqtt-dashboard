package store

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndAuthenticate(t *testing.T) {
	s := testStore(t)

	u, err := s.CreateUser("admin", "secret123", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "admin" {
		t.Errorf("username = %q, want admin", u.Username)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", u.Role, RoleAdmin)
	}

	authed, err := s.Authenticate("admin", "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if authed.ID != u.ID {
		t.Errorf("authenticated user ID mismatch")
	}
	if authed.Role != RoleAdmin {
		t.Errorf("authenticated role = %q, want %q", authed.Role, RoleAdmin)
	}
	// last_login reflects the PREVIOUS login. The first login has none, so it
	// must be nil; the second login must report the time of the first.
	if authed.LastLogin != nil {
		t.Errorf("expected last_login nil on first login, got %v", authed.LastLogin)
	}

	authed2, err := s.Authenticate("admin", "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if authed2.LastLogin == nil {
		t.Error("expected last_login to be set (previous login) on second auth")
	}
}

func TestCreateUserInvalidRole(t *testing.T) {
	s := testStore(t)
	_, err := s.CreateUser("user", "pass", "superadmin")
	if err == nil {
		t.Fatal("expected error for invalid role")
	}
}

func TestAuthenticateBadPassword(t *testing.T) {
	s := testStore(t)
	s.CreateUser("admin", "secret123", RoleAdmin)

	_, err := s.Authenticate("admin", "wrong")
	if err == nil {
		t.Fatal("expected error for bad password")
	}

	// Check failed attempt was recorded.
	u, _ := s.GetUser(1)
	if u.FailedAttempts != 1 {
		t.Errorf("failed_attempts = %d, want 1", u.FailedAttempts)
	}
	if u.LastFailedAt == nil {
		t.Error("expected last_failed_at to be set")
	}
}

func TestFailedAttemptsResetOnSuccess(t *testing.T) {
	s := testStore(t)
	s.CreateUser("admin", "pass", RoleAdmin)

	s.Authenticate("admin", "wrong")
	s.Authenticate("admin", "wrong")

	u, _ := s.GetUser(1)
	if u.FailedAttempts != 2 {
		t.Errorf("failed_attempts = %d, want 2", u.FailedAttempts)
	}

	// Successful login resets counter.
	_, err := s.Authenticate("admin", "pass")
	if err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUser(1)
	if u.FailedAttempts != 0 {
		t.Errorf("failed_attempts = %d, want 0 after success", u.FailedAttempts)
	}
}

func TestAuthenticateNoUser(t *testing.T) {
	s := testStore(t)
	_, err := s.Authenticate("nonexistent", "pass")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestChangePassword(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser("admin", "old", RoleAdmin)

	if err := s.ChangePassword(u.ID, "old", "new"); err != nil {
		t.Fatal(err)
	}

	_, err := s.Authenticate("admin", "new")
	if err != nil {
		t.Fatal("should authenticate with new password")
	}

	_, err = s.Authenticate("admin", "old")
	if err == nil {
		t.Fatal("should not authenticate with old password")
	}
}

func TestUserCount(t *testing.T) {
	s := testStore(t)

	count, err := s.UserCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	s.CreateUser("a", "p", RoleViewer)
	s.CreateUser("b", "p", RoleViewer)

	count, err = s.UserCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestListUsers(t *testing.T) {
	s := testStore(t)
	s.CreateUser("alice", "p", RoleAdmin)
	s.CreateUser("bob", "p", RoleViewer)

	users, err := s.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("len = %d, want 2", len(users))
	}
	if users[0].Username != "alice" || users[0].Role != RoleAdmin {
		t.Errorf("users[0] = %+v", users[0])
	}
	if users[1].Username != "bob" || users[1].Role != RoleViewer {
		t.Errorf("users[1] = %+v", users[1])
	}
}

func TestDeleteUser(t *testing.T) {
	s := testStore(t)
	s.CreateUser("first", "p", RoleAdmin) // id=1
	u, _ := s.CreateUser("victim", "p", RoleViewer)

	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}

	count, _ := s.UserCount()
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestDeleteDefaultAdminBlocked(t *testing.T) {
	s := testStore(t)
	s.EnsureDefaultAdmin() // creates admin with id=1

	err := s.DeleteUser(1)
	if err == nil {
		t.Fatal("expected error when deleting default admin")
	}
}

func TestDeleteUserNotFound(t *testing.T) {
	s := testStore(t)
	err := s.DeleteUser(999)
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestEnsureDefaultAdmin(t *testing.T) {
	s := testStore(t)

	u, err := s.EnsureDefaultAdmin()
	if err != nil {
		t.Fatal(err)
	}
	if u == nil {
		t.Fatal("expected user to be created")
	}
	if u.Username != "admin" || u.Role != RoleAdmin {
		t.Errorf("user = %+v, want admin/admin role", u)
	}

	// Second call should be a no-op.
	u2, err := s.EnsureDefaultAdmin()
	if err != nil {
		t.Fatal(err)
	}
	if u2 != nil {
		t.Error("expected nil on second call (users already exist)")
	}

	if !u.MustChangePassword {
		t.Error("EnsureDefaultAdmin: expected MustChangePassword=true")
	}

	// Verify we can authenticate with admin/admin.
	authed, err := s.Authenticate("admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if authed.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", authed.Role)
	}
	if !authed.MustChangePassword {
		t.Error("Authenticate: expected MustChangePassword=true for default admin")
	}

	// Verify GetUser also returns the flag.
	got, err := s.GetUser(authed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.MustChangePassword {
		t.Error("GetUser: expected MustChangePassword=true for default admin")
	}

	// After changing password, flag should be cleared.
	if err := s.ChangePassword(authed.ID, "admin", "newsecret"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUser(authed.ID)
	if got.MustChangePassword {
		t.Error("expected MustChangePassword=false after password change")
	}
}

// ---------- store.go additions ----------

func TestStoreDBGetter(t *testing.T) {
	s := testStore(t)
	db := s.DB()
	if db == nil {
		t.Fatal("DB() returned nil")
	}
}

func TestGetUserNotFound(t *testing.T) {
	s := testStore(t)
	_, err := s.GetUser(999)
	if err == nil {
		t.Fatal("expected error for non-existent user ID")
	}
}

func TestDeleteStaleMQTTBridges(t *testing.T) {
	s := testStore(t)
	env := "test-stale-env"

	// Insert a bridge with a known-old last_seen directly via the internal DB
	// handle (package-internal test). CURRENT_TIMESTAMP uses a different string
	// format than Go's time.Time, so we set last_seen explicitly to a far-past
	// value to make the comparison deterministic.
	oldTS := "2020-01-01T00:00:00Z"
	_, err := s.db.Exec(`
		INSERT INTO mqtt_bridges (env, ip, server_id, admin_url, last_seen)
		VALUES (?, ?, ?, ?, ?)`, env, "1.2.3.4", "srv-1", "http://1.2.3.4:8080", oldTS)
	if err != nil {
		t.Fatal(err)
	}

	// Also insert a fresh bridge using UpsertMQTTBridge (CURRENT_TIMESTAMP).
	if err := s.UpsertMQTTBridge(env, "2.3.4.5", "srv-2", "http://2.3.4.5:8080"); err != nil {
		t.Fatal(err)
	}

	// With a 1-year cutoff, only the old bridge should be deleted.
	cutoff := time.Hour * 24 * 365
	if err := s.DeleteStaleMQTTBridges(env, cutoff); err != nil {
		t.Fatal(err)
	}
	bridges, err := s.ListMQTTBridges(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge (the fresh one) to survive, got %d", len(bridges))
	}
	if bridges[0].IP != "2.3.4.5" {
		t.Errorf("expected fresh bridge (2.3.4.5) to survive, got %q", bridges[0].IP)
	}
}

func TestListMQTTBridgesEmpty(t *testing.T) {
	s := testStore(t)
	bridges, err := s.ListMQTTBridges("no-bridges-env")
	if err != nil {
		t.Fatal(err)
	}
	if len(bridges) != 0 {
		t.Errorf("expected 0 bridges for empty env, got %d", len(bridges))
	}
}

func TestGetTopologyPositions(t *testing.T) {
	s := testStore(t)
	env := "topo-env"

	positions := []NodePosition{
		{NodeID: "n1", X: 10.5, Y: 20.3},
		{NodeID: "n2", X: 30.0, Y: 40.0},
	}
	if err := s.SaveTopologyPositions(env, positions); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTopologyPositions(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(got))
	}

	// Results are ordered by the underlying query; build a map for comparison.
	byID := make(map[string]NodePosition)
	for _, p := range got {
		byID[p.NodeID] = p
	}
	for _, want := range positions {
		p, ok := byID[want.NodeID]
		if !ok {
			t.Errorf("node %q not found in result", want.NodeID)
			continue
		}
		if p.X != want.X || p.Y != want.Y {
			t.Errorf("node %q: got (%v,%v), want (%v,%v)", want.NodeID, p.X, p.Y, want.X, want.Y)
		}
	}
}

func TestSaveTopologyPositionsEmpty(t *testing.T) {
	s := testStore(t)
	env := "empty-topo-env"

	// Save empty positions — should succeed and clear any existing rows.
	if err := s.SaveTopologyPositions(env, nil); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTopologyPositions(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 positions after saving nil, got %d", len(got))
	}
}

func TestGetTopologyCamera(t *testing.T) {
	s := testStore(t)
	env := "cam-env"

	cam := CameraState{Zoom: 1.5, CenterX: 100.0, CenterY: 200.0}
	if err := s.SaveTopologyCamera(env, cam); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTopologyCamera(env)
	if err != nil {
		t.Fatal(err)
	}
	if got.Zoom != cam.Zoom || got.CenterX != cam.CenterX || got.CenterY != cam.CenterY {
		t.Errorf("camera round-trip: got %+v, want %+v", got, cam)
	}

	// Non-existent env should return an error (sql.ErrNoRows).
	_, err = s.GetTopologyCamera("nonexistent-env")
	if err == nil {
		t.Fatal("expected error for non-existent env")
	}
}

func TestDeleteTopologyCamera(t *testing.T) {
	s := testStore(t)
	env := "del-cam-env"

	// Deleting non-existent camera should not error.
	if err := s.DeleteTopologyCamera(env); err != nil {
		t.Fatalf("deleting non-existent camera: %v", err)
	}

	// Save and then delete.
	cam := CameraState{Zoom: 2.0, CenterX: 50.0, CenterY: 75.0}
	if err := s.SaveTopologyCamera(env, cam); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTopologyCamera(env); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// After delete, get should return an error.
	_, err := s.GetTopologyCamera(env)
	if err == nil {
		t.Fatal("expected error after camera was deleted")
	}
}

// ---------- metrics.go ----------

func TestAutoStep(t *testing.T) {
	// duration <= 0 → returns 5
	if got := autoStep(0, 0, 200); got != 5 {
		t.Errorf("autoStep(0,0,200) = %d, want 5", got)
	}
	// duration/targetPoints < 5 → clamp to 5
	if got := autoStep(0, 100, 200); got != 5 {
		t.Errorf("autoStep(0,100,200) = %d, want 5", got)
	}
	// duration/targetPoints >= 5 → use calculated step
	if got := autoStep(0, 2000, 200); got != 10 {
		t.Errorf("autoStep(0,2000,200) = %d, want 10", got)
	}
}

func TestMetricsWriterSubmitAndRun(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go w.Run(ctx)

	now := time.Now()
	cpm := int64(5)
	w.Submit(MetricSample{
		Timestamp:       now,
		Env:             "test-env",
		ServerCount:     2,
		HealthyCount:    2,
		ConnectionCount: 100,
		InMsgsRate:      10.5,
		OutMsgsRate:     20.5,
		Servers: []ServerMetricSample{{
			ServerID:    "srv-1",
			Connections: 50,
			CPU:         1.5,
			Mem:         1024,
			Healthy:     true,
		}},
		MQTTBridges: []MQTTBridgeMetricSample{{
			BridgeID:                "bridge-1",
			ConnectionsActive:       3,
			MsgsRecvQoS1:            7,
			MsgsSentQoS2:            2,
			ConsumerPendingMessages: &cpm,
		}},
	})
	time.Sleep(100 * time.Millisecond)

	from := now.Add(-time.Minute).Unix()
	to := now.Add(time.Minute).Unix()

	// Verify env metrics.
	envPts, err := w.QueryEnvMetrics(ctx, "test-env", from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(envPts) == 0 {
		t.Fatal("expected at least one env metric point")
	}

	// Verify server metrics.
	srvPts, err := w.QueryServerMetrics(ctx, "test-env", "srv-1", from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(srvPts) == 0 {
		t.Fatal("expected at least one server metric point")
	}
	if srvPts[0]["server_id"] != "srv-1" {
		t.Errorf("server_id = %v, want srv-1", srvPts[0]["server_id"])
	}

	// Verify MQTT metrics.
	mqttPts, err := w.QueryMQTTMetrics(ctx, "test-env", "bridge-1", from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mqttPts) == 0 {
		t.Fatal("expected at least one MQTT metric point")
	}
	if mqttPts[0]["bridge_id"] != "bridge-1" {
		t.Errorf("bridge_id = %v, want bridge-1", mqttPts[0]["bridge_id"])
	}
	// ConsumerPendingMessages was non-nil, so key should be present.
	if _, ok := mqttPts[0]["consumer_pending_messages"]; !ok {
		t.Error("expected consumer_pending_messages key in MQTT metric point")
	}
}

func TestMetricsWriterDeleteOld(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)

	oldTS := time.Now().Add(-25 * time.Hour)
	w.writeSample(MetricSample{
		Timestamp:   oldTS,
		Env:         "old-env",
		ServerCount: 1,
		Servers: []ServerMetricSample{{
			ServerID: "old-srv",
		}},
		MQTTBridges: []MQTTBridgeMetricSample{{
			BridgeID: "old-bridge",
		}},
	})

	w.deleteOld()

	// All three tables should be empty after deleteOld.
	pts, err := w.QueryEnvMetrics(context.Background(), "old-env", 0, oldTS.Add(time.Hour).Unix(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 0 {
		t.Errorf("expected 0 env points after deleteOld, got %d", len(pts))
	}

	srvPts, err := w.QueryServerMetrics(context.Background(), "old-env", "old-srv", 0, oldTS.Add(time.Hour).Unix(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(srvPts) != 0 {
		t.Errorf("expected 0 server points after deleteOld, got %d", len(srvPts))
	}

	mqttPts, err := w.QueryMQTTMetrics(context.Background(), "old-env", "old-bridge", 0, oldTS.Add(time.Hour).Unix(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mqttPts) != 0 {
		t.Errorf("expected 0 mqtt points after deleteOld, got %d", len(mqttPts))
	}
}

func TestQueryEnvMetricsEmpty(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)

	pts, err := w.QueryEnvMetrics(context.Background(), "no-data-env", 0, time.Now().Unix(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 0 {
		t.Errorf("expected 0 points for empty env, got %d", len(pts))
	}
}

func TestQueryServerMetricsWithServerIDFilter(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)

	now := time.Now()
	// Write two servers in the same sample.
	w.writeSample(MetricSample{
		Timestamp: now,
		Env:       "filter-env",
		Servers: []ServerMetricSample{
			{ServerID: "srv-a", Connections: 10},
			{ServerID: "srv-b", Connections: 20},
		},
	})

	from := now.Add(-time.Minute).Unix()
	to := now.Add(time.Minute).Unix()

	// Querying by srv-a should only return srv-a.
	pts, err := w.QueryServerMetrics(context.Background(), "filter-env", "srv-a", from, to, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) == 0 {
		t.Fatal("expected at least one point for srv-a")
	}
	for _, p := range pts {
		if p["server_id"] != "srv-a" {
			t.Errorf("unexpected server_id %v in filtered result", p["server_id"])
		}
	}
}

func TestQueryMQTTMetricsNullConsumerPending(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)

	now := time.Now()
	w.writeSample(MetricSample{
		Timestamp: now,
		Env:       "null-pending-env",
		MQTTBridges: []MQTTBridgeMetricSample{{
			BridgeID:                "null-bridge",
			ConnectionsActive:       1,
			ConsumerPendingMessages: nil, // nil → NULL in DB
		}},
	})

	from := now.Add(-time.Minute).Unix()
	to := now.Add(time.Minute).Unix()

	pts, err := w.QueryMQTTMetrics(context.Background(), "null-pending-env", "null-bridge", from, to, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) == 0 {
		t.Fatal("expected at least one MQTT point")
	}
	// ConsumerPendingMessages was nil, so key should be absent.
	if _, ok := pts[0]["consumer_pending_messages"]; ok {
		t.Error("expected consumer_pending_messages key to be absent when NULL")
	}
}

func TestMetricsWriterSubmitDropsWhenFull(t *testing.T) {
	s := testStore(t)
	// Do NOT start Run() — we need the channel to stay full.
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)

	sample := MetricSample{Timestamp: time.Now(), Env: "drop-env"}

	// Fill the 32-slot buffer.
	for i := 0; i < 32; i++ {
		w.Submit(sample)
	}

	// This 33rd Submit must not block and must not panic.
	done := make(chan struct{})
	go func() {
		w.Submit(sample)
		close(done)
	}()

	select {
	case <-done:
		// pass
	case <-time.After(time.Second):
		t.Error("Submit blocked instead of dropping when channel is full")
	}
}

// ---------- reachable behavior-branch tests ----------

func TestAuthenticateSetsLastLogin(t *testing.T) {
	// Authenticate twice to exercise the lastLogin.Valid path in Authenticate.
	s := testStore(t)
	s.CreateUser("alice", "pass", RoleViewer)

	// First login: last_login is NULL before, so lastLogin.Valid is false.
	_, err := s.Authenticate("alice", "pass")
	if err != nil {
		t.Fatal(err)
	}
	// Second login: last_login is now set, so lastLogin.Valid is true — exercises
	// the if lastLogin.Valid branch.
	got, err := s.Authenticate("alice", "pass")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastLogin == nil {
		t.Error("expected LastLogin to be set after second login")
	}
}

func TestListUsersWithTimestamps(t *testing.T) {
	// Populate last_login and last_failed_at so ListUsers scans the non-null paths.
	s := testStore(t)
	s.CreateUser("bob", "pass", RoleViewer)

	// Set last_failed_at by a bad password attempt.
	s.Authenticate("bob", "wrong")
	// Set last_login by a successful auth.
	s.Authenticate("bob", "pass")

	users, err := s.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].LastLogin == nil {
		t.Error("expected LastLogin to be non-nil")
	}
	// Note: after a successful auth failed_attempts resets to 0, but
	// last_failed_at remains set from the previous failure.
	if users[0].LastFailedAt == nil {
		t.Error("expected LastFailedAt to be non-nil after failed attempt")
	}
}

func TestChangePasswordWrongOldPassword(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser("carol", "correct", RoleViewer)

	err := s.ChangePassword(u.ID, "wrong-old-password", "newpass")
	if err == nil {
		t.Fatal("expected error for wrong old password")
	}
}

func TestOpenInvalidPath(t *testing.T) {
	// Pass a path under an existing file (not a directory) to trigger os.MkdirAll error.
	// Create a file and then try to use it as a directory.
	dir := t.TempDir()
	filePath := dir + "/not_a_dir"
	// Create a regular file at that path.
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Try to Open using the file as if it were a directory.
	_, err = Open(filePath + "/subdir")
	if err == nil {
		t.Fatal("expected error when dataDir cannot be created")
	}
}

func TestOpenReadOnlyDB(t *testing.T) {
	// Create a database file and make it read-only to trigger db.Ping() error.
	dir := t.TempDir()
	dbPath := dir + "/dashboard.db"

	// Create an empty file.
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Make the file read-only so SQLite can't write the WAL files.
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dbPath, 0644) })

	_, err = Open(dir)
	if err == nil {
		t.Fatal("expected error when database file is read-only")
	}
}

func TestOpenMigrateError(t *testing.T) {
	// Create a WAL-mode SQLite DB with no application tables, make it read-only.
	// On re-open, db.Ping() succeeds (WAL pragma is a no-op) but migrate()'s
	// first CREATE TABLE (users) fails with "readonly database".
	dir := migrateTestDB(t, nil) // no pre-created tables → fail at CREATE users
	_, err := Open(dir)
	if err == nil {
		t.Fatal("expected error when database is read-only for migration")
	}
}

// migrateTestDB opens a new SQLite DB in WAL mode, pre-creates the given tables,
// closes it (keeping WAL files), makes the DB file read-only, and returns the dir.
// When Open() later tries to open this DB, db.Ping() succeeds (WAL mode already
// set, pragma is a no-op) but migrate() fails on the first missing table.
func migrateTestDB(t *testing.T, preTables []string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/dashboard.db"

	// Use the same WAL DSN as Open() does.
	dsn := "file:" + dbPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	for _, tbl := range preTables {
		if _, err := db.Exec(tbl); err != nil {
			db.Close()
			t.Fatalf("pre-create table: %v", err)
		}
	}
	db.Close()
	// Keep WAL/SHM files — they are needed so Ping() works on re-open.
	// Make the DB file read-only; WAL/SHM files stay writable (or read-only too, both work).
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dbPath, 0644) })
	return dir
}

// schemaUsers and schemaClusters are minimal table schemas that match what
// migrate() creates, so CREATE TABLE IF NOT EXISTS is a no-op on them.
const schemaUsers = `CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'viewer',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	last_login DATETIME,
	failed_attempts INTEGER NOT NULL DEFAULT 0,
	last_failed_at DATETIME,
	must_change_password INTEGER NOT NULL DEFAULT 0
)`
const schemaClusters = `CREATE TABLE IF NOT EXISTS clusters (
	id TEXT PRIMARY KEY, name TEXT NOT NULL,
	servers TEXT NOT NULL DEFAULT '[]',
	mqtt_bridges TEXT NOT NULL DEFAULT '[]',
	mqtt_discovery TEXT, tls TEXT,
	admin_token TEXT NOT NULL DEFAULT '',
	nats_conn TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
const schemaMQTTBridges = `CREATE TABLE IF NOT EXISTS mqtt_bridges (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	env TEXT NOT NULL, ip TEXT NOT NULL, server_id TEXT NOT NULL,
	admin_url TEXT NOT NULL DEFAULT '',
	last_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(env, ip, server_id)
)`
const schemaServerMetrics = `CREATE TABLE IF NOT EXISTS server_metrics (
	ts INTEGER NOT NULL, env TEXT NOT NULL, server_id TEXT NOT NULL,
	connections INTEGER, in_msgs INTEGER, out_msgs INTEGER,
	in_bytes INTEGER, out_bytes INTEGER, cpu REAL, mem INTEGER,
	subscriptions INTEGER, slow_consumers INTEGER, routes INTEGER, leafnodes INTEGER,
	in_msgs_rate REAL, out_msgs_rate REAL, in_bytes_rate REAL, out_bytes_rate REAL, healthy INTEGER
)`
const schemaEnvMetrics = `CREATE TABLE IF NOT EXISTS env_metrics (
	ts INTEGER NOT NULL, env TEXT NOT NULL,
	server_count INTEGER, healthy_count INTEGER, connection_count INTEGER,
	in_msgs_rate REAL, out_msgs_rate REAL, in_bytes_rate REAL, out_bytes_rate REAL, subscriptions INTEGER
)`
const schemaMQTTBridgeMetrics = `CREATE TABLE IF NOT EXISTS mqtt_bridge_metrics (
	ts INTEGER NOT NULL, env TEXT NOT NULL, bridge_id TEXT NOT NULL,
	connections_active INTEGER, in_msgs_rate REAL, out_msgs_rate REAL,
	in_bytes_rate REAL, out_bytes_rate REAL,
	msgs_recv_qos0 INTEGER, msgs_recv_qos1 INTEGER, msgs_sent_qos0 INTEGER, msgs_sent_qos1 INTEGER,
	msgs_recv_qos2 INTEGER, msgs_sent_qos2 INTEGER,
	session_write_behind_depth INTEGER, consumer_pending_messages INTEGER, stalled_consumers INTEGER
)`
const schemaTopologyPositions = `CREATE TABLE IF NOT EXISTS topology_positions (
	env TEXT NOT NULL, node_id TEXT NOT NULL, x REAL NOT NULL, y REAL NOT NULL,
	PRIMARY KEY (env, node_id)
)`

func TestOpenMigrateErrorUsersExists(t *testing.T) {
	// Pre-create users → migrate errors at CREATE TABLE clusters.
	dir := migrateTestDB(t, []string{schemaUsers})
	if _, err := Open(dir); err == nil {
		t.Fatal("expected migrate error when clusters table cannot be created")
	}
}

func TestOpenMigrateErrorUsersAndClustersExist(t *testing.T) {
	// Pre-create users+clusters → migrate errors at CREATE TABLE mqtt_bridges.
	dir := migrateTestDB(t, []string{schemaUsers, schemaClusters})
	if _, err := Open(dir); err == nil {
		t.Fatal("expected migrate error when mqtt_bridges table cannot be created")
	}
}

func TestOpenMigrateErrorUpToMQTTBridges(t *testing.T) {
	// Pre-create users+clusters+mqtt_bridges → errors at server_metrics.
	dir := migrateTestDB(t, []string{schemaUsers, schemaClusters, schemaMQTTBridges})
	if _, err := Open(dir); err == nil {
		t.Fatal("expected migrate error when server_metrics table cannot be created")
	}
}

func TestOpenMigrateErrorUpToServerMetrics(t *testing.T) {
	// Pre-create through server_metrics → errors at env_metrics.
	dir := migrateTestDB(t, []string{schemaUsers, schemaClusters, schemaMQTTBridges, schemaServerMetrics})
	if _, err := Open(dir); err == nil {
		t.Fatal("expected migrate error when env_metrics table cannot be created")
	}
}

func TestOpenMigrateErrorUpToEnvMetrics(t *testing.T) {
	// Pre-create through env_metrics → errors at mqtt_bridge_metrics.
	dir := migrateTestDB(t, []string{schemaUsers, schemaClusters, schemaMQTTBridges, schemaServerMetrics, schemaEnvMetrics})
	if _, err := Open(dir); err == nil {
		t.Fatal("expected migrate error when mqtt_bridge_metrics table cannot be created")
	}
}

func TestOpenMigrateErrorUpToMQTTBridgeMetrics(t *testing.T) {
	// Pre-create through mqtt_bridge_metrics → errors at topology_positions.
	dir := migrateTestDB(t, []string{schemaUsers, schemaClusters, schemaMQTTBridges, schemaServerMetrics, schemaEnvMetrics, schemaMQTTBridgeMetrics})
	if _, err := Open(dir); err == nil {
		t.Fatal("expected migrate error when topology_positions table cannot be created")
	}
}

func TestOpenMigrateErrorUpToTopologyPositions(t *testing.T) {
	// Pre-create through topology_positions → errors at topology_camera.
	dir := migrateTestDB(t, []string{schemaUsers, schemaClusters, schemaMQTTBridges, schemaServerMetrics, schemaEnvMetrics, schemaMQTTBridgeMetrics, schemaTopologyPositions})
	if _, err := Open(dir); err == nil {
		t.Fatal("expected migrate error when topology_camera table cannot be created")
	}
}

func TestWriteSampleCommitError(t *testing.T) {
	// Use a deferred FK constraint on env_metrics to make tx.Commit() fail
	// after all INSERTs succeed, covering the "metrics tx commit" Warn branch.
	s := testStore(t)

	// Enable FK enforcement on this connection.
	if _, err := s.db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	// Create a reference table for env values.
	if _, err := s.db.Exec("CREATE TABLE env_ref (env TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	// Replace env_metrics with a version that has a deferred FK to env_ref.
	// This lets the INSERT succeed but causes COMMIT to fail when the FK is checked.
	if _, err := s.db.Exec("DROP TABLE env_metrics"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE env_metrics (
		ts INTEGER NOT NULL, env TEXT NOT NULL
			REFERENCES env_ref(env) DEFERRABLE INITIALLY DEFERRED,
		server_count INTEGER, healthy_count INTEGER, connection_count INTEGER,
		in_msgs_rate REAL, out_msgs_rate REAL, in_bytes_rate REAL, out_bytes_rate REAL,
		subscriptions INTEGER
	)`); err != nil {
		t.Fatal(err)
	}

	w := NewMetricsWriter(s.DB(), slog.Default(), 0)
	// writeSample: Begin + INSERT env_metrics succeed (FK check is deferred),
	// but Commit fails because env_ref has no matching row.
	w.writeSample(MetricSample{
		Timestamp:   time.Now(),
		Env:         "no-such-env-in-ref",
		ServerCount: 1,
	})
	// The function should handle the error gracefully (slog.Warn), not panic.
}

func TestWriteSampleDroppedEnvTable(t *testing.T) {
	// Drop env_metrics to cover the "metrics insert env" warn+return branch.
	s := testStore(t)
	if _, err := s.db.Exec("DROP TABLE env_metrics"); err != nil {
		t.Fatal(err)
	}
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)
	// writeSample: tx.Begin succeeds, env_metrics INSERT fails.
	w.writeSample(MetricSample{Timestamp: time.Now(), Env: "e", ServerCount: 1})
}

func TestWriteSampleDroppedServerTable(t *testing.T) {
	// Drop server_metrics to cover the "metrics insert server" warn branch.
	// The env insert succeeds, but server insert fails.
	s := testStore(t)
	if _, err := s.db.Exec("DROP TABLE server_metrics"); err != nil {
		t.Fatal(err)
	}
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)
	w.writeSample(MetricSample{
		Timestamp: time.Now(),
		Env:       "e",
		Servers: []ServerMetricSample{{ServerID: "srv-1"}},
	})
}

func TestWriteSampleDroppedMQTTTable(t *testing.T) {
	// Drop mqtt_bridge_metrics to cover the "metrics insert mqtt" warn branch.
	s := testStore(t)
	if _, err := s.db.Exec("DROP TABLE mqtt_bridge_metrics"); err != nil {
		t.Fatal(err)
	}
	w := NewMetricsWriter(s.DB(), slog.Default(), 0)
	w.writeSample(MetricSample{
		Timestamp:   time.Now(),
		Env:         "e",
		MQTTBridges: []MQTTBridgeMetricSample{{BridgeID: "b1"}},
	})
}

func TestSaveTopologyPositionsDroppedTable(t *testing.T) {
	// Drop the topology_positions table to trigger the tx.Exec DELETE error
	// branch inside SaveTopologyPositions (line 536-538).
	s := testStore(t)
	if _, err := s.db.Exec("DROP TABLE topology_positions"); err != nil {
		t.Fatal(err)
	}
	err := s.SaveTopologyPositions("e", []NodePosition{{NodeID: "n1", X: 1, Y: 2}})
	if err == nil {
		t.Error("expected error when topology_positions table does not exist")
	}
}

func TestSaveTopologyPositionsPrepareError(t *testing.T) {
	// Replace the topology_positions table with a schema missing the 'y' column
	// so that tx.Prepare("INSERT INTO topology_positions (env, node_id, x, y)...")
	// fails after the DELETE succeeds, covering the Prepare-error branch.
	s := testStore(t)

	// Drop and recreate with wrong schema (no 'y' column).
	if _, err := s.db.Exec("DROP TABLE topology_positions"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE topology_positions (
		env TEXT NOT NULL, node_id TEXT NOT NULL, x REAL NOT NULL,
		PRIMARY KEY (env, node_id)
	)`); err != nil {
		t.Fatal(err)
	}

	err := s.SaveTopologyPositions("e", []NodePosition{{NodeID: "n1", X: 1.0, Y: 2.0}})
	if err == nil {
		t.Error("expected error when topology_positions table is missing 'y' column")
	}
}

func TestSaveTopologyPositionsDuplicateNodeID(t *testing.T) {
	// Pass duplicate node_ids to trigger the stmt.Exec error inside the loop
	// (PRIMARY KEY constraint violation on (env, node_id)).
	s := testStore(t)
	positions := []NodePosition{
		{NodeID: "dup", X: 1.0, Y: 2.0},
		{NodeID: "dup", X: 3.0, Y: 4.0}, // same node_id → constraint violation
	}
	err := s.SaveTopologyPositions("dup-env", positions)
	if err == nil {
		t.Error("expected error for duplicate node_id in positions")
	}
}

func TestDeleteClusterDeleteError(t *testing.T) {
	// Block DELETE on clusters to cover the tx.Exec error branch in DeleteCluster
	// (line 192-194). The trigger fires, causing RAISE(ABORT) before any row is deleted.
	s := testStore(t)
	cl := makeCluster("blocked-del", "http://x")
	if err := s.CreateCluster(cl); err != nil {
		t.Fatal(err)
	}

	_, err := s.db.Exec(`
		CREATE TRIGGER block_cluster_delete
		BEFORE DELETE ON clusters
		BEGIN SELECT RAISE(ABORT, 'test: delete blocked'); END
	`)
	if err != nil {
		t.Fatal(err)
	}

	err = s.DeleteCluster(cl.ID)
	if err == nil {
		t.Error("expected error when DELETE on clusters is blocked")
	}
}

func TestDeleteClusterCascadeError(t *testing.T) {
	// Drop one of the cascade tables to trigger the cascade DELETE error branch
	// in DeleteCluster.
	s := testStore(t)
	cl := makeCluster("cascade-err", "http://x")
	if err := s.CreateCluster(cl); err != nil {
		t.Fatal(err)
	}

	// Drop a cascade table (mqtt_bridges) so the loop hits an error.
	if _, err := s.db.Exec("DROP TABLE mqtt_bridges"); err != nil {
		t.Fatal(err)
	}

	err := s.DeleteCluster(cl.ID)
	if err == nil {
		t.Error("expected error when cascade table is missing")
	}
}

func TestAuthenticateUpdateFailedAttemptsError(t *testing.T) {
	// Block the UPDATE that records failed_attempts to cover the slog.Warn
	// "failed to record login attempt" branch.
	s := testStore(t)
	s.CreateUser("dave", "pass", RoleViewer)

	// Install a trigger that blocks UPDATE of failed_attempts.
	_, err := s.db.Exec(`
		CREATE TRIGGER block_failed_attempts_update
		BEFORE UPDATE OF failed_attempts ON users
		BEGIN SELECT RAISE(ABORT, 'test: update blocked'); END
	`)
	if err != nil {
		t.Fatal(err)
	}
	// Authenticate with wrong password — the UPDATE for failed_attempts will fail
	// (slog.Warn fires), but Authenticate still returns "invalid credentials" error.
	_, err = s.Authenticate("dave", "wrong")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestAuthenticateUpdateLastLoginError(t *testing.T) {
	// Block the UPDATE that sets last_login to cover the slog.Warn
	// "failed to update login timestamp" branch.
	s := testStore(t)
	s.CreateUser("eve", "pass", RoleViewer)

	// Install a trigger that blocks UPDATE of last_login.
	_, err := s.db.Exec(`
		CREATE TRIGGER block_last_login_update
		BEFORE UPDATE OF last_login ON users
		BEGIN SELECT RAISE(ABORT, 'test: update blocked'); END
	`)
	if err != nil {
		t.Fatal(err)
	}
	// Correct password — the UPDATE for last_login will fail (slog.Warn fires),
	// but Authenticate still returns the user.
	u, err := s.Authenticate("eve", "pass")
	if err != nil {
		t.Fatalf("expected success even when last_login update fails: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil user")
	}
}

func TestCreateUserInsertError(t *testing.T) {
	// Block INSERT on users to cover the "insert user" error branch in CreateUser.
	s := testStore(t)
	_, err := s.db.Exec(`
		CREATE TRIGGER block_user_insert
		BEFORE INSERT ON users
		BEGIN SELECT RAISE(ABORT, 'test: insert blocked'); END
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateUser("frank", "pass", RoleViewer)
	if err == nil {
		t.Fatal("expected error when INSERT is blocked")
	}
}

func TestEnsureDefaultAdminCreateUserError(t *testing.T) {
	// Install a BEFORE INSERT trigger on users that raises an error, so that
	// EnsureDefaultAdmin's CreateUser call fails while UserCount returns 0.
	s := testStore(t)
	_, err := s.db.Exec(`
		CREATE TRIGGER block_admin_insert
		BEFORE INSERT ON users
		BEGIN SELECT RAISE(ABORT, 'test: insert blocked'); END
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.EnsureDefaultAdmin()
	if err == nil {
		t.Fatal("expected error from EnsureDefaultAdmin when CreateUser fails")
	}
}

func TestEnsureDefaultAdminUpdateError(t *testing.T) {
	// After CreateUser succeeds, block the UPDATE so the must_change_password
	// SET fails. Install a trigger only for UPDATE.
	s := testStore(t)

	// First, set up the trigger that blocks UPDATE on must_change_password.
	_, err := s.db.Exec(`
		CREATE TRIGGER block_admin_update
		BEFORE UPDATE OF must_change_password ON users
		BEGIN SELECT RAISE(ABORT, 'test: update blocked'); END
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.EnsureDefaultAdmin()
	if err == nil {
		t.Fatal("expected error from EnsureDefaultAdmin when UPDATE fails")
	}
}

func TestDeleteUserZeroRowsAffected(t *testing.T) {
	// Add a BEFORE DELETE trigger with RAISE(IGNORE) so that DELETE succeeds
	// with 0 rows affected, covering the n==0 error branch in DeleteUser.
	s := testStore(t)
	u, _ := s.CreateUser("ghost", "pass", RoleViewer)

	// Trigger fires and silently ignores the delete → RowsAffected() == 0.
	_, err := s.db.Exec(`
		CREATE TRIGGER ignore_user_delete
		BEFORE DELETE ON users
		BEGIN SELECT RAISE(IGNORE); END
	`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DeleteUser(u.ID)
	if err == nil {
		t.Fatal("expected error when DELETE affects 0 rows")
	}
}

func TestDeleteUserExecError(t *testing.T) {
	// Install a BEFORE DELETE trigger to make the DELETE fail after the SELECT
	// succeeds (covering the Exec error branch in DeleteUser).
	s := testStore(t)
	u, _ := s.CreateUser("victim", "pass", RoleViewer)

	// Add trigger AFTER creating the user.
	_, err := s.db.Exec(`
		CREATE TRIGGER block_user_delete
		BEFORE DELETE ON users
		BEGIN SELECT RAISE(ABORT, 'test: delete blocked'); END
	`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DeleteUser(u.ID)
	if err == nil {
		t.Fatal("expected error from DeleteUser when DELETE is blocked")
	}
}

func TestMarshalNullable(t *testing.T) {
	// nil (untyped) → v == nil should be true → returns sql.NullString{}
	result, err := marshalNullable(nil)
	if err != nil {
		t.Fatalf("marshalNullable(nil) error: %v", err)
	}
	if result.Valid {
		t.Error("marshalNullable(nil): expected invalid NullString")
	}

	// Unmarshalable type (channel) → json.Marshal error.
	ch := make(chan int)
	_, err = marshalNullable(ch)
	if err == nil {
		t.Error("marshalNullable(chan): expected error for unmarshalable type")
	}
}

func TestScanClusterBadJSON(t *testing.T) {
	// Insert rows with bad JSON directly to cover the unmarshal-error branches
	// in scanCluster. These branches are otherwise unreachable via the public API.
	s := testStore(t)

	// Insert a cluster with invalid servers JSON.
	_, err := s.db.Exec(`INSERT INTO clusters (id, name, servers, mqtt_bridges, created_at)
		VALUES ('bad-srv', 'bad', 'not-json', '[]', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetCluster("bad-srv")
	if err == nil {
		t.Error("expected error when servers JSON is invalid")
	}

	// Insert a cluster with invalid mqtt_bridges JSON.
	_, err = s.db.Exec(`INSERT INTO clusters (id, name, servers, mqtt_bridges, created_at)
		VALUES ('bad-bridges', 'bad', '[]', 'not-json', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetCluster("bad-bridges")
	if err == nil {
		t.Error("expected error when mqtt_bridges JSON is invalid")
	}

	// Insert a cluster with invalid mqtt_discovery JSON.
	_, err = s.db.Exec(`INSERT INTO clusters (id, name, servers, mqtt_bridges, mqtt_discovery, created_at)
		VALUES ('bad-disc', 'bad', '[]', '[]', 'not-json', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetCluster("bad-disc")
	if err == nil {
		t.Error("expected error when mqtt_discovery JSON is invalid")
	}

	// Insert a cluster with invalid tls JSON.
	_, err = s.db.Exec(`INSERT INTO clusters (id, name, servers, mqtt_bridges, tls, created_at)
		VALUES ('bad-tls', 'bad', '[]', '[]', 'not-json', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetCluster("bad-tls")
	if err == nil {
		t.Error("expected error when tls JSON is invalid")
	}

	// Insert a cluster with invalid nats_conn JSON.
	_, err = s.db.Exec(`INSERT INTO clusters (id, name, servers, mqtt_bridges, nats_conn, created_at)
		VALUES ('bad-nats', 'bad', '[]', '[]', 'not-json', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetCluster("bad-nats")
	if err == nil {
		t.Error("expected error when nats_conn JSON is invalid")
	}
}

func TestListClustersWithBadJSON(t *testing.T) {
	// Also exercise scanCluster errors through ListClusters path (different code path).
	s := testStore(t)

	_, err := s.db.Exec(`INSERT INTO clusters (id, name, servers, mqtt_bridges, created_at)
		VALUES ('lc-bad', 'bad', 'INVALID', '[]', CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ListClusters()
	if err == nil {
		t.Error("expected error from ListClusters when servers JSON is invalid")
	}
}

// ---------- additional store/clusters error-branch coverage ----------

// testClosedStore returns a *Store whose underlying DB has already been closed.
// This lets us exercise every "return err" branch that requires a DB failure.
func testClosedStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Close now so all subsequent DB operations fail.
	s.db.Close()
	return s
}

func TestClosedStoreErrors(t *testing.T) {
	s := testClosedStore(t)

	if _, err := s.UserCount(); err == nil {
		t.Error("UserCount: expected error on closed DB")
	}
	if _, err := s.CreateUser("u", "p", RoleViewer); err == nil {
		t.Error("CreateUser: expected error on closed DB")
	}
	if _, err := s.Authenticate("u", "p"); err == nil {
		t.Error("Authenticate: expected error on closed DB")
	}
	if err := s.ChangePassword(1, "old", "new"); err == nil {
		t.Error("ChangePassword: expected error on closed DB")
	}
	if _, err := s.GetUser(1); err == nil {
		t.Error("GetUser: expected error on closed DB")
	}
	if _, err := s.ListUsers(); err == nil {
		t.Error("ListUsers: expected error on closed DB")
	}
	if err := s.DeleteUser(1); err == nil {
		t.Error("DeleteUser: expected error on closed DB")
	}
	if _, err := s.EnsureDefaultAdmin(); err == nil {
		t.Error("EnsureDefaultAdmin: expected error on closed DB")
	}
	if err := s.UpsertMQTTBridge("e", "1.2.3.4", "s", ""); err == nil {
		t.Error("UpsertMQTTBridge: expected error on closed DB")
	}
	if _, err := s.ListMQTTBridges("e"); err == nil {
		t.Error("ListMQTTBridges: expected error on closed DB")
	}
	if err := s.DeleteStaleMQTTBridges("e", time.Hour); err == nil {
		t.Error("DeleteStaleMQTTBridges: expected error on closed DB")
	}
	if _, err := s.GetTopologyPositions("e"); err == nil {
		t.Error("GetTopologyPositions: expected error on closed DB")
	}
	if _, err := s.GetTopologyCamera("e"); err == nil {
		t.Error("GetTopologyCamera: expected error on closed DB")
	}
	if err := s.SaveTopologyCamera("e", CameraState{}); err == nil {
		t.Error("SaveTopologyCamera: expected error on closed DB")
	}
	if err := s.DeleteTopologyCamera("e"); err == nil {
		t.Error("DeleteTopologyCamera: expected error on closed DB")
	}
	if err := s.SaveTopologyPositions("e", []NodePosition{{NodeID: "n1", X: 1, Y: 2}}); err == nil {
		t.Error("SaveTopologyPositions: expected error on closed DB")
	}
}

func TestClosedStoreClusters(t *testing.T) {
	s := testClosedStore(t)

	if err := s.CreateCluster(&Cluster{Name: "x", Servers: []config.Server{{URL: "http://x"}}}); err == nil {
		t.Error("CreateCluster: expected error on closed DB")
	}
	if _, err := s.ListClusters(); err == nil {
		t.Error("ListClusters: expected error on closed DB")
	}
	if _, err := s.GetCluster("nope"); err == nil {
		t.Error("GetCluster: expected error on closed DB")
	}
	if err := s.UpdateCluster(&Cluster{ID: "x", Name: "x", Servers: []config.Server{{URL: "http://x"}}}); err == nil {
		t.Error("UpdateCluster: expected error on closed DB")
	}
	if err := s.DeleteCluster("x"); err == nil {
		t.Error("DeleteCluster: expected error on closed DB")
	}
	if _, err := s.ClusterCount(); err == nil {
		t.Error("ClusterCount: expected error on closed DB")
	}
}

// ---------- error-branch coverage: closed-DB writer ----------

func TestMetricsWriterClosedDB(t *testing.T) {
	// Open a fresh SQLite database, run the schema, then close it so every
	// subsequent call returns an error — this covers the Warn/return branches
	// inside writeSample, deleteOld, and the three Query* functions.
	dir := t.TempDir()
	// Use a temporary store just to get a migrated DB file.
	tmp, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db := tmp.db // grab the handle before Close
	tmp.Close()  // close the Store (and therefore the DB)

	w := NewMetricsWriter(db, slog.Default(), 0)

	// writeSample: Begin will fail — covers the "metrics tx begin" warn + return.
	w.writeSample(MetricSample{Timestamp: time.Now(), Env: "e"})

	// deleteOld: Exec will fail — covers the "metrics cleanup" warn per table.
	w.deleteOld()

	ctx := context.Background()

	// Query* funcs: QueryContext will fail — covers their error returns.
	_, err = w.QueryEnvMetrics(ctx, "e", 0, 100, 5)
	if err == nil {
		t.Error("QueryEnvMetrics: expected error on closed DB")
	}
	_, err = w.QueryServerMetrics(ctx, "e", "", 0, 100, 5)
	if err == nil {
		t.Error("QueryServerMetrics: expected error on closed DB")
	}
	_, err = w.QueryMQTTMetrics(ctx, "e", "", 0, 100, 5)
	if err == nil {
		t.Error("QueryMQTTMetrics: expected error on closed DB")
	}
}
