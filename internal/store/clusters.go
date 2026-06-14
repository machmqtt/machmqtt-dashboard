package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// Cluster is a NATS cluster managed via the admin UI.
// The ID is stable and used as the key throughout the system (metrics, topology,
// WS routing, API paths). The Name is a mutable display label.
type Cluster struct {
	ID            string                      `json:"id"`
	Name          string                      `json:"name"`
	Servers       []config.Server             `json:"servers"`
	MQTTBridges   []config.MQTTBridge         `json:"mqtt_bridges"`
	MQTTDiscovery *config.MQTTDiscoveryConfig `json:"mqtt_discovery,omitempty"`
	TLS           *config.TLSConfig           `json:"tls,omitempty"`
	AdminToken    string                      `json:"admin_token,omitempty"`
	NATSConn      *config.NATSConnConfig      `json:"nats_conn,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
}

// ToEnvironment converts a Cluster to the config.Environment shape consumed by
// the collector/fetcher/MQTT handlers. The env.Name is the cluster Name (display
// label); the caller should pass the cluster ID as the key everywhere else.
func (c *Cluster) ToEnvironment() config.Environment {
	return config.Environment{
		Name:          c.Name,
		Servers:       c.Servers,
		MQTTBridges:   c.MQTTBridges,
		MQTTDiscovery: c.MQTTDiscovery,
		AdminToken:    c.AdminToken,
		TLS:           c.TLS,
		NATSConn:      c.NATSConn,
	}
}

// generateClusterID returns a 12-character hex string from crypto/rand.
func generateClusterID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate cluster id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CreateCluster persists a new cluster and populates c.ID and c.CreatedAt.
func (s *Store) CreateCluster(c *Cluster) error {
	if c.Name == "" {
		return fmt.Errorf("cluster name is required")
	}
	if len(c.Servers) == 0 {
		return fmt.Errorf("at least one server URL is required")
	}

	id, err := generateClusterID()
	if err != nil {
		return err
	}
	c.ID = id

	cols, err := marshalClusterFields(c)
	if err != nil {
		return err
	}

	now := time.Now()
	_, err = s.db.Exec(
		`INSERT INTO clusters (id, name, servers, mqtt_bridges, mqtt_discovery, tls, admin_token, nats_conn, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, cols.servers, cols.bridges, cols.discovery, cols.tls, c.AdminToken, cols.natsConn, now,
	)
	if err != nil {
		return fmt.Errorf("insert cluster: %w", err)
	}
	c.CreatedAt = now
	return nil
}

// ListClusters returns all clusters ordered by name.
func (s *Store) ListClusters() ([]Cluster, error) {
	rows, err := s.db.Query(
		`SELECT id, name, servers, mqtt_bridges, mqtt_discovery, tls, admin_token, nats_conn, created_at
		 FROM clusters ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clusters []Cluster
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

// GetCluster returns a single cluster by ID, or sql.ErrNoRows.
func (s *Store) GetCluster(id string) (*Cluster, error) {
	row := s.db.QueryRow(
		`SELECT id, name, servers, mqtt_bridges, mqtt_discovery, tls, admin_token, nats_conn, created_at
		 FROM clusters WHERE id = ?`, id,
	)
	c, err := scanCluster(row)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateCluster updates an existing cluster's mutable fields.
func (s *Store) UpdateCluster(c *Cluster) error {
	if c.Name == "" {
		return fmt.Errorf("cluster name is required")
	}
	if len(c.Servers) == 0 {
		return fmt.Errorf("at least one server URL is required")
	}

	cols, err := marshalClusterFields(c)
	if err != nil {
		return err
	}

	result, err := s.db.Exec(
		`UPDATE clusters SET name=?, servers=?, mqtt_bridges=?, mqtt_discovery=?, tls=?, admin_token=?, nats_conn=?
		 WHERE id=?`,
		c.Name, cols.servers, cols.bridges, cols.discovery, cols.tls, c.AdminToken, cols.natsConn, c.ID,
	)
	if err != nil {
		return fmt.Errorf("update cluster: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cluster not found")
	}
	return nil
}

// DeleteCluster removes a cluster and cascades to all 6 env-keyed data tables
// in a single transaction. The cluster ID is used as the env key in those tables.
func (s *Store) DeleteCluster(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec("DELETE FROM clusters WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete cluster: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cluster not found")
	}

	// Cascade to all tables that key on env = cluster ID.
	for _, table := range []string{
		"mqtt_bridges",
		"server_metrics",
		"env_metrics",
		"mqtt_bridge_metrics",
		"topology_positions",
		"topology_camera",
	} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE env = ?", id); err != nil {
			return fmt.Errorf("cascade delete %s: %w", table, err)
		}
	}

	return tx.Commit()
}

// ClusterCount returns the total number of clusters.
func (s *Store) ClusterCount() (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM clusters").Scan(&n)
	return n, err
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanCluster(row scanner) (Cluster, error) {
	var c Cluster
	var serversJSON, bridgesJSON string
	var discoveryJSON, tlsJSON, natsConnJSON sql.NullString
	if err := row.Scan(&c.ID, &c.Name, &serversJSON, &bridgesJSON, &discoveryJSON, &tlsJSON, &c.AdminToken, &natsConnJSON, &c.CreatedAt); err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(serversJSON), &c.Servers); err != nil {
		return c, fmt.Errorf("unmarshal servers: %w", err)
	}
	if err := json.Unmarshal([]byte(bridgesJSON), &c.MQTTBridges); err != nil {
		return c, fmt.Errorf("unmarshal mqtt_bridges: %w", err)
	}
	if discoveryJSON.Valid && discoveryJSON.String != "" && discoveryJSON.String != "null" {
		c.MQTTDiscovery = &config.MQTTDiscoveryConfig{}
		if err := json.Unmarshal([]byte(discoveryJSON.String), c.MQTTDiscovery); err != nil {
			return c, fmt.Errorf("unmarshal mqtt_discovery: %w", err)
		}
	}
	if tlsJSON.Valid && tlsJSON.String != "" && tlsJSON.String != "null" {
		c.TLS = &config.TLSConfig{}
		if err := json.Unmarshal([]byte(tlsJSON.String), c.TLS); err != nil {
			return c, fmt.Errorf("unmarshal tls: %w", err)
		}
	}
	if natsConnJSON.Valid && natsConnJSON.String != "" && natsConnJSON.String != "null" {
		c.NATSConn = &config.NATSConnConfig{}
		if err := json.Unmarshal([]byte(natsConnJSON.String), c.NATSConn); err != nil {
			return c, fmt.Errorf("unmarshal nats_conn: %w", err)
		}
	}
	return c, nil
}

// clusterCols holds the JSON-encoded column values shared by the cluster INSERT
// and UPDATE statements.
type clusterCols struct {
	servers   string
	bridges   string
	discovery sql.NullString
	tls       sql.NullString
	natsConn  sql.NullString
}

// marshalClusterFields encodes the variable-shape cluster columns once so
// CreateCluster and UpdateCluster don't repeat the marshal sequence.
func marshalClusterFields(c *Cluster) (clusterCols, error) {
	var cc clusterCols
	servers, err := json.Marshal(c.Servers)
	if err != nil {
		return cc, fmt.Errorf("marshal servers: %w", err)
	}
	cc.servers = string(servers)
	bridges, err := json.Marshal(nullableSlice(c.MQTTBridges))
	if err != nil {
		return cc, fmt.Errorf("marshal mqtt_bridges: %w", err)
	}
	cc.bridges = string(bridges)
	if cc.discovery, err = marshalNullable(c.MQTTDiscovery); err != nil {
		return cc, fmt.Errorf("marshal mqtt_discovery: %w", err)
	}
	if cc.tls, err = marshalNullable(c.TLS); err != nil {
		return cc, fmt.Errorf("marshal tls: %w", err)
	}
	if cc.natsConn, err = marshalNullable(c.NATSConn); err != nil {
		return cc, fmt.Errorf("marshal nats_conn: %w", err)
	}
	return cc, nil
}

// marshalNullable marshals v to a JSON string, or returns sql.NullString{}
// (NULL) when v is nil.
func marshalNullable(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// nullableSlice returns an empty non-nil slice when s is nil, so JSON output is
// always [] rather than null.
func nullableSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
