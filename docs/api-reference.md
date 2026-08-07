# API Reference

All endpoints are served from the dashboard's HTTP server (default `:8080`).

## Authentication & Authorization

Authentication uses JWT tokens stored in an `httpOnly` cookie named `session`. The cookie is set on successful login and cleared on logout.

The public authentication endpoints are `POST /api/login`, `POST /api/auth/local/login`, `GET /api/auth/providers`, and the OIDC login/callback routes. Other `/api/*` endpoints require authentication. Operational endpoints are `GET /livez` and `GET /readyz` (both unauthenticated, for probes) and `GET /metrics`, which requires either the configured `metrics_token` as a bearer token or a dashboard session. Errors are JSON and every response includes `X-Request-ID`.

### Roles

Users have one of two roles:

- **admin** — Full access. Can create and delete users via `/api/admin/*` endpoints.
- **viewer** — Read-only access to all monitoring data. Cannot manage users.

Admin endpoints (`/api/admin/*`) return `403 Forbidden` for non-admin users.

---

## Auth Endpoints

### POST /api/login

Authenticate and receive a session cookie.

External LDAP providers are evaluated in configured order. Local authentication is attempted only if no external provider contains the identity.

**Request body:**
```json
{
  "username": "admin",
  "password": "<operator-supplied-bootstrap-password>"
}
```

**Response (200):**
```json
{
  "id": 1,
  "username": "admin",
  "role": "admin",
  "auth_provider": "local",
  "created_at": "2026-03-20T10:00:00Z"
}
```

Sets a `session` httpOnly cookie.

**Response (401):** Invalid credentials.

**Response (429):** Too many login attempts from this client IP, or this account is
temporarily locked out after repeated consecutive failures. Retry after the window
elapses.

### POST /api/logout

Clear the session cookie.

**Response (200):**
```json
{ "ok": true }
```

### GET /api/me

Get the current authenticated user.

**Response (200):**
```json
{
  "id": 1,
  "username": "admin",
  "role": "admin",
  "auth_provider": "local",
  "created_at": "2026-03-20T10:00:00Z"
}
```

### PUT /api/users/{id}/password

Change a user's password. Users can only change their own password.

**Request body:**
```json
{
  "old_password": "current",
  "new_password": "new-password"
}
```

**Response (200):**
```json
{ "ok": true }
```

**Response (403):** Trying to change another user's password.

---

## Admin Endpoints

These endpoints require the `admin` role. Non-admin users receive `403 Forbidden`.

### GET /api/admin/users

List all users.

**Response (200):**
```json
{
  "users": [
    {
      "id": 1,
      "username": "admin",
      "role": "admin",
      "created_at": "2026-03-20T10:00:00Z"
    },
    {
      "id": 2,
      "username": "viewer1",
      "role": "viewer",
      "created_at": "2026-03-20T12:00:00Z"
    }
  ]
}
```

### POST /api/admin/users

Create a new user. Admin role required.

**Request body:**
```json
{
  "username": "newuser",
  "password": "secure-password",
  "role": "viewer"
}
```

`role` is optional, defaults to `viewer`. Valid values: `admin`, `viewer`.

**Response (201):**
```json
{
  "id": 2,
  "username": "newuser",
  "role": "viewer",
  "created_at": "2026-03-20T12:00:00Z"
}
```

### DELETE /api/admin/users/{id}

Delete a user. Cannot delete your own account, and cannot delete the default admin
account (`id=1`, username `admin`) even from another admin's session.

**Response (200):**
```json
{ "ok": true }
```

**Response (400):** Attempting to delete your own account, attempting to delete the
default admin account, an invalid (non-numeric) `id`, or the user was not found — all
of these currently return `400`, not `404`.

### Cluster Management

Full CRUD over the clusters ("environments") the dashboard collects from. Changes apply
to the live collector immediately, with no restart. `{id}` is the database-generated
cluster ID.

Secrets (`admin_token`, and any `nats_conn`/bridge auth field) are **never** echoed back
by these endpoints — reads return `has_*` booleans in their place. On create/update, a
blank secret field means "leave the stored value unchanged"; a non-empty value
overwrites it.

#### GET /api/admin/clusters

List all clusters with their full (secret-redacted) configuration.

**Response:**
```json
{
  "clusters": [
    {
      "id": "a1b2c3d4e5f6",
      "name": "production",
      "servers": [{ "url": "http://nats-1:8222" }],
      "mqtt_bridges": [{ "name": "edge-1", "url": "http://bridge-1:8080", "has_bearer_token": true }],
      "mqtt_discovery": { "enabled": true, "admin_ports": [8080], "trusted_hosts": [] },
      "tls": { "ca_file": "", "ca_pem": "", "insecure": false },
      "has_admin_token": true,
      "nats_conn": {
        "urls": ["nats://nats-1:4222"],
        "username": "monitor",
        "subject_prefix": "$MQTT5",
        "sys_collection": true,
        "has_password": true,
        "has_token": false,
        "has_nkey": false,
        "has_creds": false
      },
      "created_at": "2026-03-20T10:00:00Z"
    }
  ]
}
```

#### POST /api/admin/clusters

Create a cluster. Body: same shape as one entry above, but with plaintext secret fields
(`admin_token`, `nats_conn.password`/`token`/`nkey`,
`mqtt_bridges[].bearer_token`) instead of `has_*` booleans.

**Filesystem paths are config-file-only.** `tls.ca_file`, `nats_conn.tls.ca_file` and
`nats_conn.creds_file` name files on the dashboard host, so the API refuses to set them:
honouring a client-chosen path would let the admin role probe the host filesystem and
exhaust the process on a device file such as `/dev/zero`. Pin a custom CA over the API with
`tls.ca_pem`, which carries the PEM bundle inline. Omitting these fields (or resubmitting the
value the `GET` response showed) keeps whatever the config file declared.

**Response (201):** The created cluster, in the redacted `GET` shape.

**Response (400):** Missing `name`, no `servers` entries, an attempt to set a host path, or a
`ca_pem` that contains no usable PEM certificates.

#### PUT /api/admin/clusters/{id}

Update a cluster's configuration.

**Response (200):** The updated cluster, in the redacted `GET` shape.

**Response (400):** Same validation as create.

**Response (404):** Cluster not found.

#### DELETE /api/admin/clusters/{id}

Delete a cluster. Stops its collector and cascades deletion of its associated MQTT
bridge, metrics, and topology rows.

**Response (200):** `{ "ok": true }`

**Response (404):** Cluster not found.

### GET /api/admin/logs

Recent buffered server log entries (in-memory ring buffer), newest first. Powers the
in-UI Server Logs page.

**Response:**
```json
{ "logs": [{ "time": "2026-03-20T10:00:00Z", "level": "INFO", "msg": "...", "...": "..." }] }
```

### GET /api/admin/health

The dashboard's own operational health: overall `status` (`"ok"` or `"degraded"`),
WebSocket client/drop counters, dropped-metrics-sample counter, and a per-cluster
diagnostic array (poll age, `$SYS` fallback state, NATS-push connectivity, staleness).

**Response:**
```json
{
  "status": "ok",
  "ws_clients": 3,
  "ws_stale_clients": 0,
  "ws_dropped_total": 0,
  "dropped_samples": 0,
  "clusters": [
    {
      "id": "a1b2c3d4e5f6",
      "name": "production",
      "last_poll_age_seconds": 4.2,
      "servers": 3,
      "healthy_servers": 3,
      "collection_mode": "http",
      "sys_fallback_engaged": false,
      "nats_push_configured": false,
      "nats_push_connected": false,
      "stale": false
    }
  ]
}
```

---

## System Endpoints

### GET /healthz

Unauthenticated liveness/readiness probe (for a load balancer or Kubernetes). Returns
`200 {"status":"ok"}` when the database is reachable, `503 {"status":"db_unavailable"}`
otherwise.

### GET /api/version

The running dashboard's version string.

**Response:** `{ "version": "v1.2.3" }`

---

## Environment Endpoints

All environment data endpoints are under `/api/environments/{env}/` where `{env}` is the
**database-generated cluster ID** (a 12-character hex string, e.g. `a1b2c3d4e5f6`) — not
the cluster's display name from the config file or admin UI. Use `GET /api/environments`
to discover each cluster's ID.

### GET /api/environments

List all clusters, with lightweight per-cluster health for the sidebar.

**Response:**
```json
{
  "environments": [
    {
      "id": "a1b2c3d4e5f6",
      "name": "production",
      "degraded": false,
      "degraded_reason": "",
      "collection_mode": "http",
      "last_poll_age_seconds": 4.2
    }
  ]
}
```

`collection_mode` is `"http"`, `"sys"`, or `"sys-fallback"` (`$SYS` push temporarily
degraded to HTTP polling). `degraded` and `degraded_reason` are set when the collector
considers the cluster's data stale or unhealthy; the full diagnostic detail is available
to admins via `GET /api/admin/health`.

### GET /api/environments/{env}/overview

Aggregated overview with server summaries.

**Response:**
```json
{
  "server_count": 3,
  "healthy_count": 3,
  "connection_count": 150,
  "in_msgs_rate": 1500.5,
  "out_msgs_rate": 1200.3,
  "in_bytes_rate": 50000,
  "out_bytes_rate": 40000,
  "subscriptions": 800,
  "js_streams": 5,
  "js_consumers": 12,
  "js_messages": 100000,
  "js_bytes": 5000000,
  "servers": [
    {
      "id": "NABC123",
      "name": "nats-1",
      "version": "2.11.0",
      "connections": 50,
      "cpu": 12.5,
      "mem": 1048576,
      "in_msgs_rate": 500.0,
      "out_msgs_rate": 400.0,
      "healthy": true,
      "uptime": "24h5m"
    }
  ]
}
```

### GET /api/environments/{env}/topology

Force-graph data for the cluster topology visualization.

**Response:**
```json
{
  "nodes": [
    {
      "id": "NABC123",
      "name": "nats-1",
      "type": "server",
      "connections": 50,
      "healthy": true,
      "in_msgs_rate": 500.0,
      "out_msgs_rate": 400.0,
      "cluster": "dc1"
    }
  ],
  "links": [
    {
      "source": "NABC123",
      "target": "NDEF456",
      "type": "route",
      "in_msgs_rate": 100.0,
      "out_msgs_rate": 80.0
    }
  ]
}
```

Node types: `server`, `gateway`, `leaf`.
Link types: `route`, `gateway`, `leaf`.

### GET /api/environments/{env}/topology/positions

Persisted node positions and camera (pan/zoom) state for the topology graph, so a
manually arranged layout survives a reload.

**Response:**
```json
{
  "positions": [
    { "node_id": "NABC123", "x": 120.5, "y": -40.2 }
  ],
  "camera": { "zoom": 1.2, "center_x": 0, "center_y": 0 }
}
```

### PUT /api/environments/{env}/topology/positions

Save node positions and (optionally) camera state.

**Request body:**
```json
{
  "positions": [{ "node_id": "NABC123", "x": 120.5, "y": -40.2 }],
  "camera": { "zoom": 1.2, "center_x": 0, "center_y": 0 }
}
```

**Response (200):** `{ "status": "ok" }`

### GET /api/environments/{env}/varz

Per-server variable data, keyed by server ID.

**Response:**
```json
{
  "NABC123": {
    "server_id": "NABC123",
    "server_name": "nats-1",
    "version": "2.11.0",
    "host": "0.0.0.0",
    "port": 4222,
    "connections": 50,
    "in_msgs": 100000,
    "out_msgs": 80000,
    "in_bytes": 5000000,
    "out_bytes": 4000000,
    "mem": 1048576,
    "cpu": 12.5,
    "cores": 4,
    "subscriptions": 300,
    "uptime": "24h5m"
  }
}
```

### GET /api/environments/{env}/connz

Paginated connections list, aggregated across all servers from the cached snapshot (not
fetched live from NATS).

**Query parameters:**

| Parameter        | Type   | Default | Description |
|------------------|--------|---------|-------------|
| `limit`          | int    | 50      | Max connections to return, capped at 10000 |
| `offset`         | int    | 0       | Pagination offset, capped at 100000 |
| `acc`            | string | —       | Filter by account name |
| `filter_subject` | string | —       | Filter to connections with a subscription containing this substring (uses a 15s-TTL subscription-detail cache; adds a `subs_available` field to the response) |

**Response:**
```json
{
  "connections": [
    {
      "cid": 5,
      "ip": "192.168.1.10",
      "port": 54321,
      "name": "my-service",
      "account": "$G",
      "authorized_user": "admin",
      "rtt": "1.5ms",
      "in_msgs": 1000,
      "out_msgs": 800,
      "in_bytes": 50000,
      "out_bytes": 40000,
      "subscriptions": 5,
      "uptime": "1h30m",
      "lang": "go",
      "version": "1.36.0"
    }
  ],
  "total": 150,
  "limit": 50,
  "offset": 0
}
```

`total` is the sum reported by upstream NATS servers. `loaded_total` is the bounded number materialized by the dashboard. Results have stable server-ID/CID ordering. If a server fails or a safety cap truncates materialization, `partial` is true and `failed_servers` reports the unavailable-server count.

### GET /api/environments/{env}/connz/{cid}

Single connection detail by CID, using complete bounded upstream pagination.

**Response:** A single connection object (same fields as above).

### GET /api/environments/{env}/routez

Cluster route information, keyed by server ID.

### GET /api/environments/{env}/gatewayz

Gateway connections, keyed by server ID.

### GET /api/environments/{env}/leafz

Leaf node connections, keyed by server ID.

### GET /api/environments/{env}/subsz

Subscription statistics per server.

**Response:**
```json
{
  "NABC123": {
    "server_id": "NABC123",
    "num_subscriptions": 300,
    "num_cache": 100,
    "num_inserts": 5000,
    "num_removes": 4700,
    "num_matching": 10000,
    "cache_hit_rate": 85,
    "max_fanout": 10,
    "avg_fanout": 2.5
  }
}
```

### GET /api/environments/{env}/subsz/detail

Flat, filterable table of individual subscriptions (subject, queue, sid, connection,
account, server), backed by the same 15s-TTL subscription-detail cache as the `/connz`
`filter_subject` parameter.

**Query parameters:**

| Parameter     | Type   | Default | Description |
|---------------|--------|---------|-------------|
| `limit`       | int    | 100     | Max rows to return, capped at 10000 |
| `offset`      | int    | 0       | Pagination offset, capped at 100000 |
| `subject`     | string | —       | Filter to subjects containing this substring |
| `account`     | string | —       | Filter by account name |
| `server`      | string | —       | Filter by server name or ID |
| `hide_system` | bool   | `false` | Exclude system subjects (`_`/`$`-prefixed, excluding `$MQTT5.*`) when `true` |

**Response:**
```json
{
  "subscriptions": [
    {
      "subject": "orders.>",
      "queue": "",
      "sid": "12",
      "msgs": 42,
      "conn_cid": 5,
      "conn_name": "my-service",
      "conn_ip": "192.168.1.10",
      "account": "$G",
      "server_id": "NABC123",
      "server_name": "nats-1"
    }
  ],
  "total": 300,
  "limit": 100,
  "offset": 0
}
```

### GET /api/environments/{env}/jsz

JetStream information with stream and consumer details, keyed by server ID.

### GET /api/environments/{env}/accountz

Account list, keyed by server ID.

### GET /api/environments/{env}/accountz/{acc}

Detailed information for a single account, computed from the cached snapshot (connz,
leafz, and the subscription-detail cache) — not fetched live.

**Response:**
```json
{
  "account_name": "$G",
  "is_system": false,
  "expired": false,
  "jetstream_enabled": true,
  "leafnode_connections": 0,
  "client_connections": 50,
  "subscriptions": 200
}
```

**Response (404):** Account not found (not present in any server's account list, and no
connections, leaf nodes, or subscriptions reference it).

---

## MQTT Bridge Endpoints

Endpoints for monitoring [MachMQTT](https://machmqtt.com) bridge instances discovered or
configured on a cluster. `{bridge}` is a bridge's name. Detail routes prefer the cached
NATS-push metrics snapshot for a bridge and fall back to a single live HTTP request to
the bridge's admin API only when no push data exists yet for it; a push-only bridge with
no configured admin URL returns a `{"available": false, "reason": ...}` (or equivalent)
payload for routes that require the admin API, rather than an error.

### GET /api/environments/{env}/mqtt/bridges

List all bridges for a cluster — both auto-discovered and statically configured —
merged into one entry per bridge.

### GET /api/environments/{env}/mqtt/{bridge}/connz

Paginated list of the bridge's MQTT client connections (admin API only).

**Query parameters:** `limit` (default 50, max 10000), `offset` (default 0, max 100000).

### GET /api/environments/{env}/mqtt/{bridge}/connz/{client}

Single MQTT client's connection detail by client ID (admin API only).

### GET /api/environments/{env}/mqtt/{bridge}/diag

The bridge's own NATS-connection diagnostics: connection/reconnect status and the
connected server, plus its account's JetStream streams and KV buckets, if any.

### GET /api/environments/{env}/mqtt/{bridge}/diag/config

The bridge's running configuration summary (admin API only).

### GET /api/environments/{env}/mqtt/{bridge}/license

The bridge's license status (tier, validity) as reported by the bridge's admin API.

### GET /api/environments/{env}/mqtt/{bridge}/metrics

The bridge's metrics counters (connections, message/byte rates, QoS breakdown, pool and
queue depths) — from the push snapshot when available, otherwise scraped live from the
bridge's Prometheus-format `/metrics` endpoint.

### GET /api/environments/{env}/mqtt/{bridge}/pool

The bridge's connection-pool status (admin API preferred, push snapshot fallback).

### GET /api/environments/{env}/mqtt/{bridge}/cluster

The bridge's cluster membership status, when the bridge has clustering enabled.

**Response:** `{"available": true, "cluster": {...}}`, or `{"available": false, "reason": "..."}` when clustering isn't enabled, the bridge doesn't support the endpoint, or admin auth failed.

### GET /api/environments/{env}/mqtt/{bridge}/cluster/inspect

Locates a single MQTT client across the bridge's cluster.

**Query parameters:** `client_id` (required).

**Response:** `{"found": true, "inspect": {...}}`, or `{"found": false, "reason": "..."}`.

### POST /api/environments/{env}/mqtt/{bridge}/admin/{action}

Proxies a state-changing admin action to the bridge. **Admin role required.** `{action}`
is one of: `drain`, `undrain`, `reload`, `kick-all-clients`, `cluster-kick-client`,
`cluster-kick-by-username`, `cluster-kick-all`. The bridge's response status and body are
relayed as-is (e.g. `403` if the bridge has the action disabled, `409` if clustering
isn't enabled for a cluster-kick action).

**Response (400):** Unknown `{action}`.

**Response (404):** Bridge not found.

**Response (503):** The bridge was only discovered via NATS push (no admin URL
configured), so admin actions aren't reachable.

---

## Time-Series Metrics Endpoints

Historical trend data backed by SQLite (`server_metrics`, `env_metrics`,
`mqtt_bridge_metrics`), retained for `metrics_retention` (default 24h). All three accept
`from`, `to` (Unix seconds, default: last 1 hour, max 30 days of history), and `step`
(bucket size in seconds; auto-clamped so a query never returns more than 5000 points).

### GET /api/environments/{env}/metrics/overview

Cluster-wide aggregate time series (connection count, message/byte rates, etc.).

### GET /api/environments/{env}/metrics/servers

Per-server time series. Optional `server_id` query parameter filters to one server.

### GET /api/environments/{env}/metrics/mqtt

Per-bridge time series. Optional `bridge_id` query parameter filters to one bridge.

**Response (all three):**
```json
{ "points": [{ "ts": 1735689600, "...": "metric fields..." }] }
```

**Response (503):** The metrics store is unavailable.

---

## WebSocket

### GET /api/ws

Upgrade to WebSocket for real-time data updates.

**Client -> Server:** Subscribe to a cluster, by ID:
```json
{ "subscribe": "a1b2c3d4e5f6" }
```

**Server -> Client:** The server pushes messages on each poll cycle:

```json
{ "type": "overview", "env": "a1b2c3d4e5f6", "data": { ... } }
{ "type": "topology", "env": "a1b2c3d4e5f6", "data": { ... } }
{ "type": "health",   "env": "a1b2c3d4e5f6", "data": { ... } }
```

Message types:
- `overview` — Summary numbers (same shape as `/api/environments/{env}/overview`)
- `topology` — Full graph (same shape as `/api/environments/{env}/topology`)
- `health` — Per-server health status map

The WebSocket connection supports ping/pong keepalive (54s interval, 60s timeout).

To switch environments, send a new subscribe message. Only one environment subscription is active per connection.

---

## Static Assets

All non-`/api/` paths serve the embedded React SPA. Unknown paths fall back to `index.html` for client-side routing.
