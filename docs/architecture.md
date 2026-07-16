# Architecture

## Overview

MachMQTT Dashboard is a single-binary web application that monitors NATS clusters (and
MachMQTT MQTT bridges running on them) and embeds a React SPA. Each cluster is collected
via HTTP polling by default, with an optional NATS client connection for push-based
collection — the two paths converge on the same in-memory snapshot and SQLite
time-series store.

```
 Browser
   React SPA (Overview, Topology, Connections, JetStream, Accounts, MQTT Fleet, Admin)
       ↕ REST API              ↕ WebSocket
 ───────────────────────────────────────────────────────────────────────────
 Go HTTP Server
   Auth (JWT)   API handlers   WS Hub (broadcast)
        │             │              │
        └──────┬──────┴──────┬───────┘
               ▼              ▼
          SQLite (Store)   Collector Manager
                              ├── Collector (Cluster 1)
                              ├── Collector (Cluster 2)
                              └── ...
                                  │
                    ┌─────────────┴──────────────┐
                    ▼                             ▼
        HTTP :8222 (monitoring, default)   NATS :4222 (optional push:
                                            $SYS stats / MQTT metrics)
                    └─────────────┬──────────────┘
                                  ▼
                            NATS Cluster(s)
```

## Components

### Collector (`internal/collector/`)

The collector is the core data engine. `nats.go` and `nats-server/v2` are **direct**
dependencies (see `go.mod`) — the collector imports both, for push-based collection and
for the in-process test server used by integration tests.

**Manager** — Owns one `Collector` per cluster stored in the database (clusters are
added, edited, and removed at runtime via the admin API; `AddCluster`/`UpdateCluster`/
`RemoveCluster` start, reconfigure, and stop the corresponding collector goroutine
live). Provides `Snapshot()`, `Overview()`, `Topology()`, `Health()`, `ClusterHealth()`,
and `HealthReport()` accessors keyed by cluster ID. Fires an `onChange` callback after
each poll cycle to trigger WebSocket broadcasts and a metrics-sample write.

**Collector** — One goroutine per cluster. Runs a ticker at the configured
`poll_interval`. Each cluster independently uses one of two collection paths for server
data:

- **HTTP polling** (default, always-available fallback) — `errgroup`-fetches all
  configured servers' monitoring endpoints concurrently, two-tier:
  - Fast tier (every cycle): `/varz`, `/routez`, `/gatewayz`, `/leafz`, `/healthz`, `/connz`
  - Slow tier (every 3rd cycle): `/subsz`, `/jsz`, `/accountz` — carried forward between slow polls
- **`$SYS` push collection** (`sys_collector.go`, enabled by `nats_conn.sys_collection`)
  — subscribes to `$SYS.SERVER.*.STATSZ` for continuous per-server stats and uses a
  `$SYS.REQ.SERVER.PING.*` fan-in for topology/connection detail. If it stops producing
  data (no NATS connection, no system-account access), the collector automatically falls
  back to HTTP polling — a cold start falls back immediately, a mid-flight outage rides
  out a short grace period first — and resumes `$SYS` automatically once data reappears.

MachMQTT bridge fleet data uses a separate, independent pair of paths:

- **NATS push** (`mqtt_subscriber.go`, enabled by `nats_conn`) — subscribes to
  `<subject_prefix>.metrics.>` (default prefix `$MQTT5`); bridges self-announce and are
  held in a short-TTL cache, aging out automatically if a bridge stops publishing. This
  is the preferred source whenever it has data.
- **connz-scan discovery + HTTP probe** (`mqtt_discovery.go`) — used whenever the push
  subscriber has no bridges yet (HTTP-only clusters, push still warming up, or `$SYS`
  fallback): scans `/connz` for connections that look like MachMQTT bridges, then probes
  each candidate host's admin API. A per-bridge HTTP fetcher (`mqtt_fetcher.go`) also
  scrapes each bridge's Prometheus-format `/metrics` endpoint and its admin API
  (diagnostics, license, pool, cluster status) on demand for the detail pages.

**MachMQTT version compatibility.** Both bridge ingestion paths are designed to
work across MachMQTT versions, in both directions:

- *Newer broker, older dashboard (forward):* additive broker changes are safe.
  The push payload is JSON, so unknown fields at any nesting level are dropped by
  `encoding/json`; the Prometheus parser ignores metric families it doesn't
  recognize, and unknown label reasons on known families (e.g. a new
  `auth_failure_total{reason=...}`) still count toward the family's total. A
  *breaking* wire change is signaled by the broker bumping the payload's `v`
  field; the subscriber skips messages with `v` greater than its supported
  schema (`bridgeMetricsSchemaV`) and logs a one-time "upgrade the dashboard"
  warning rather than misinterpreting them. Admin endpoints a bridge doesn't
  expose yet degrade per-tab (the relayed 404/409 is shown as "not supported by
  this bridge version"), not per-page.
- *Older broker, newer dashboard (backward):* legacy publishers that omit `v`
  (`v=0`) are accepted, a payload without an embedded `metrics` object falls
  back to safe defaults (JetStream-absent sentinel `-1` for consumer pending),
  and metrics the old broker doesn't emit simply read zero. Scrapes from older
  Prometheus surfaces parse the same way.

These guarantees are pinned by tests in `internal/collector/mqtt_compat_test.go`.

**Fetcher** — HTTP client with per-request timeouts. Supports optional TLS (custom CA or
insecure skip). One Fetcher per cluster.

**Snapshot** — Point-in-time view of all data for one cluster. Includes computed
msg/byte rates as deltas from the previous snapshot.

**Topology** — Builds a force-graph from the snapshot: nodes from `/varz` (servers),
`/gatewayz` (remote gateways), `/leafz` (leaf nodes); edges from `/routez` (deduplicated
bidirectional), `/gatewayz`, `/leafz`.

### Cluster Management (`internal/store/clusters.go`, `internal/api/handlers_clusters.go`)

Clusters (the API and UI call them "environments") are rows in the `clusters` table,
keyed by a generated 12-character hex ID — **not** their display name. `{env}` in every
`/api/environments/{env}/...` route is this ID.

- `SeedClusters` (called once at startup) creates any cluster declared under the
  config file's `environments:` key that isn't already present, matched by name; it
  never touches a cluster that already exists, so runtime edits are permanent.
- The admin-only `/api/admin/clusters` endpoints support full CRUD at runtime: create,
  update, and delete a cluster's servers, TLS, MQTT bridges/discovery, and `nats_conn`,
  applying changes to the live collector without a restart.
- Secrets (`admin_token`, and `nats_conn`/bridge auth) are never echoed back in API
  responses — read endpoints return a redacted view (`has_password`, `has_token`, …
  booleans instead of values), and an update with a blank secret field means "keep the
  stored value."

### Store (`internal/store/`)

SQLite database via `modernc.org/sqlite` (pure Go, no CGO). Opened with WAL journaling
and `synchronous=normal` for concurrent reads without an fsync per commit, and
incremental `auto_vacuum` so space freed by retention pruning can be returned to the OS
(a database created before 1.0 is converted to this mode automatically on first open).
Eight tables:

| Table | Holds |
|-------|-------|
| `users` | Accounts, bcrypt password hashes, roles, lockout/failed-attempt state |
| `clusters` | Cluster config: servers, MQTT bridges/discovery, TLS, `admin_token`, `nats_conn` (plaintext at rest — see [Security Considerations](deployment.md#security-considerations)) |
| `mqtt_bridges` | Discovered MachMQTT bridge sightings (connz-scan path) |
| `server_metrics` | Per-server time-series samples |
| `env_metrics` | Per-cluster aggregate time-series samples |
| `mqtt_bridge_metrics` | Per-bridge time-series samples |
| `topology_positions` | User-dragged node positions on the topology graph |
| `topology_camera` | Saved pan/zoom state per cluster |

Auto-creates the default admin user on first run if no users exist.

### Time-Series Metrics (`internal/store/metrics.go`, `internal/collector/metrics_sample.go`)

A `MetricsWriter` batches one sample per cluster per poll cycle (built from the
collector's `onChange` callback) into `server_metrics`, `env_metrics`, and
`mqtt_bridge_metrics`. A background cleanup pass deletes samples older than
`metrics_retention` (default 24h). Queried via `/api/environments/{env}/metrics/*` for
trend charts.

### Auth (`internal/auth/`)

- Passwords hashed with bcrypt
- JWT tokens signed with HMAC-SHA256 using the configured `session_secret`
- Tokens stored in `httpOnly`, `SameSite=Strict` cookies (24h TTL)
- Per-IP login rate limiting, plus a per-account lockout after repeated consecutive
  failures — both surfaced to `POST /api/login` as `429 Too Many Requests`
- `token_version` is bumped on password change / logout so other outstanding sessions
  for that user are invalidated
- Middleware extracts and validates tokens, re-reading current role/token-version from
  the database so a stale JWT can't outlive a revoked/role-changed/forced-password-reset
  account

### API (`internal/api/`)

Uses Go stdlib routing patterns (`http.ServeMux` with method+path patterns). No
third-party router.

- Public: `POST /api/login`, `GET /healthz`
- Protected: all other `/api/*` routes wrapped with auth middleware
- Admin-only: `/api/admin/*`, plus the MQTT bridge admin-action route, wrapped with an
  additional role check
- SPA: all non-API paths serve the embedded React build with `index.html` fallback

All NATS and MQTT bridge reads are served from the cached in-memory snapshot the
collector maintains — a request never triggers a live fetch to a NATS server just to
answer it. The two narrow exceptions: the connections page's subscription-subject
filter and the `/subsz/detail` view share a 15-second-TTL cache that, on a miss, gathers
subscription detail from the snapshot (or a background HTTP scan on HTTP-only clusters)
rather than the primary per-poll snapshot; and MQTT bridge detail routes (diagnostics,
license, pool, cluster status) prefer the cached NATS-push snapshot and fall back to one
live HTTP fetch only when no push data exists yet for that bridge.

### WebSocket (`internal/ws/`)

Hub pattern with per-client goroutines:

- **Hub** — Maintains the set of connected clients. `Broadcast()` sends messages to all
  clients subscribed to a specific cluster.
- **Client** — Two goroutines per connection (read pump + write pump). Clients send
  `{"subscribe":"<clusterID>"}` to select their cluster. The write pump handles
  ping/pong keepalive (54s interval, 60s timeout). A client whose send buffer stays
  full (a slow or backgrounded viewer) has messages dropped rather than blocking the
  broadcast, and is force-closed after a sustained run of drops so the browser
  reconnects and resyncs from a fresh snapshot instead of showing frozen data. Drop
  counts are surfaced at `GET /api/admin/health`.

Messages are small summaries pushed on each poll cycle. The UI fetches full paginated
data via REST when needed.

### Server Logs (`internal/logbuf/`)

An in-memory ring buffer wraps the process's `slog` handler, capturing recent log lines
for the in-UI Server Logs page (`GET /api/admin/logs`), independent of wherever stderr
is also being sent.

### Frontend (`ui/`)

React 19 + TypeScript + Vite + Tailwind CSS.

**State:** Zustand store holds active cluster, overview, topology, health, dark mode
preference, sidebar state, and toast queue.

**Data flow:**
1. On login, the app fetches `/api/environments` and sets the first as active
2. `useWebSocket` hook connects to `/api/ws` and subscribes to the active cluster
3. WS messages update the zustand store, which re-renders Overview and Topology pages in real-time
4. Other pages (Connections, JetStream, Accounts, MQTT Fleet) fetch data via REST on mount/filter change

**Topology visualization:** Uses `react-force-graph-2d` (D3 force simulation) with
custom canvas rendering for node shapes and animated particles for message flow.
**Trend charts:** Uses `recharts` for the time-series metrics views.

## Data Flow Diagram

```
                    ┌── HTTP :8222 ──> Fetcher ──┐
NATS Cluster ───────┤                            ├──> Collector ──> Snapshot
                    └── NATS :4222 (optional) ───┘         │
                                                            ├──> Manager.Overview()  ──> WS Hub ──> Browser
                                                            ├──> Manager.Topology()  ──> WS Hub ──> Browser
                                                            ├──> Manager.Health()    ──> WS Hub ──> Browser
                                                            ├──> MetricsWriter       ──> SQLite (server_metrics, env_metrics, mqtt_bridge_metrics)
                                                            └──> API handlers        ──> REST response ──> Browser
```

## Dependencies

### Go

| Package                          | Purpose                                            |
|-----------------------------------|-----------------------------------------------------|
| `gopkg.in/yaml.v3`                | Config file parsing                                |
| `modernc.org/sqlite`              | Store: users, clusters, MQTT bridges, metrics, topology (pure Go, no CGO) |
| `github.com/golang-jwt/jwt/v5`    | Session tokens                                     |
| `golang.org/x/crypto`             | bcrypt password hashing                            |
| `github.com/gorilla/websocket`    | WebSocket connections                              |
| `golang.org/x/sync`               | errgroup for concurrent HTTP fetching              |
| `github.com/nats-io/nats.go`      | NATS client connection for push collection (`$SYS`, MQTT bridge metrics subscription) |
| `github.com/nats-io/nats-server/v2` | In-process NATS server for collector integration tests (`internal/testutil/natstest`) |

### Frontend

| Package                | Purpose                        |
|------------------------|---------------------------------|
| `react-router-dom`     | Client-side routing            |
| `@tanstack/react-table`| Data tables with pagination    |
| `react-force-graph-2d` | Topology graph visualization   |
| `recharts`             | Time-series trend charts       |
| `zustand`              | State management               |
| `tailwindcss`          | Styling                        |
| `lucide-react`         | Icons                          |

## Build Output

The Vite build outputs to `internal/api/dist/`. The Go binary embeds this directory via `//go:embed dist/*`. The result is a single static binary with no external file dependencies (except the config file and data directory).
