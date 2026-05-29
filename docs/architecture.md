# Architecture

## Overview

MachMQTT Dashboard is a single-binary web application that monitors NATS clusters by polling their HTTP monitoring endpoints. It consists of a Go backend that embeds a React SPA.

```
┌─────────────────────────────────────────────┐
│                 Browser                      │
│  React SPA (Overview, Topology, Connections) │
│         ↕ REST API    ↕ WebSocket            │
├─────────────────────────────────────────────┤
│              Go HTTP Server                  │
│  ┌─────────┐  ┌──────────┐  ┌────────────┐ │
│  │  Auth    │  │  API     │  │  WS Hub    │ │
│  │ (JWT)    │  │ handlers │  │ (broadcast)│ │
│  └────┬────┘  └────┬─────┘  └─────┬──────┘ │
│       │            │               │         │
│  ┌────┴────┐  ┌────┴───────────────┴──┐     │
│  │  Store  │  │    Collector Manager   │     │
│  │(SQLite) │  │  ┌─────┐ ┌─────┐     │     │
│  └─────────┘  │  │Env 1│ │Env 2│ ... │     │
│               │  └──┬──┘ └──┬──┘     │     │
│               └─────┼───────┼────────┘     │
├─────────────────────┼───────┼──────────────┤
│               ┌─────┴───┐ ┌─┴──────┐       │
│               │ NATS    │ │ NATS   │       │
│               │Cluster 1│ │Cluster 2│       │
│               │:8222    │ │:8222   │       │
│               └─────────┘ └────────┘       │
└─────────────────────────────────────────────┘
```

## Components

### Collector (`internal/collector/`)

The collector is the core data engine. It does not import `nats-server` or `nats.go` — it uses only HTTP to fetch data from the NATS monitoring endpoints.

**Manager** — Owns one Collector per configured environment. Provides `Snapshot()`, `Overview()`, `Topology()`, and `Health()` accessors. Fires an `onChange` callback after each poll cycle to trigger WebSocket broadcasts.

**Collector** — One goroutine per environment. Runs a ticker at the configured `poll_interval`. Uses `errgroup` to fetch all servers in an environment concurrently.

**Two-tier polling:**
- Fast tier (every interval): `/varz`, `/routez`, `/gatewayz`, `/leafz`, `/healthz`
- Slow tier (every 3rd interval): `/connz`, `/subsz`, `/jsz`, `/accountz`

Between slow polls, the previous slow-tier data is carried forward.

**Fetcher** — HTTP client with 3-second per-request timeouts. Supports optional TLS (custom CA or insecure skip). One Fetcher per environment.

**Snapshot** — Point-in-time view of all data for one environment. Includes computed msg/byte rates as deltas from the previous snapshot.

**Topology** — Builds a force-graph from the snapshot:
- Nodes from `/varz` (servers), `/gatewayz` (remote gateways), `/leafz` (leaf nodes)
- Edges from `/routez` (deduplicated bidirectional), `/gatewayz`, `/leafz`

### Types (`internal/collector/types.go`)

Minimal Go structs with JSON tags matching NATS monitoring responses. Only the fields the dashboard needs are defined. This avoids importing `nats-server` and its large dependency tree.

### Auth (`internal/auth/`)

- Passwords hashed with bcrypt
- JWT tokens signed with HMAC-SHA256 using the configured `session_secret`
- Tokens stored in `httpOnly`, `SameSite=Strict` cookies (24h TTL)
- Middleware extracts and validates tokens, injects claims into request context

### Store (`internal/store/`)

SQLite database via `modernc.org/sqlite` (pure Go, no CGO). Single `users` table. WAL mode for concurrent reads. Auto-creates the default admin user on first run if no users exist.

### API (`internal/api/`)

Uses Go 1.22+ stdlib routing patterns (`http.ServeMux` with method+path patterns). No third-party router.

- Public: `POST /api/login`
- Protected: All other `/api/*` routes wrapped with auth middleware
- SPA: All non-API paths serve the embedded React build with `index.html` fallback

The `/connz` endpoint fetches live data from NATS servers on each request (not from the cache) to support real-time filtering and pagination parameters. All other data endpoints serve from the cached snapshot.

### WebSocket (`internal/ws/`)

Hub pattern with per-client goroutines:

- **Hub** — Maintains a set of connected clients. `Broadcast()` sends messages to all clients subscribed to a specific environment.
- **Client** — Two goroutines per connection (read pump + write pump). Clients send `{"subscribe":"envName"}` to select their environment. The write pump handles ping/pong keepalive.

Messages are small summaries pushed on each poll cycle. The UI fetches full paginated data via REST when needed.

### Frontend (`ui/`)

React 19 + TypeScript + Vite + Tailwind CSS.

**State:** Zustand store holds active environment, overview, topology, health, dark mode preference, sidebar state, and toast queue.

**Data flow:**
1. On login, the app fetches `/api/environments` and sets the first as active
2. `useWebSocket` hook connects to `/api/ws` and subscribes to the active environment
3. WS messages update the zustand store, which re-renders Overview and Topology pages in real-time
4. Other pages (Connections, JetStream, Accounts) fetch data via REST on mount/filter change

**Topology visualization:** Uses `react-force-graph-2d` (D3 force simulation) with custom canvas rendering for node shapes (circles, diamonds, triangles) and animated particles for message flow.

## Data Flow Diagram

```
NATS :8222 ──HTTP──> Fetcher ──> Collector ──> Snapshot
                                     │
                                     ├──> Manager.Overview() ──> WS Hub ──> Browser
                                     ├──> Manager.Topology() ──> WS Hub ──> Browser
                                     ├──> Manager.Health()   ──> WS Hub ──> Browser
                                     │
                                     └──> API handlers ──> REST response ──> Browser
```

## Dependencies

### Go

| Package                       | Purpose                              |
|-------------------------------|--------------------------------------|
| `gopkg.in/yaml.v3`           | Config file parsing                  |
| `modernc.org/sqlite`         | User store (pure Go, no CGO)         |
| `github.com/golang-jwt/jwt/v5` | Session tokens                     |
| `golang.org/x/crypto`        | bcrypt password hashing              |
| `github.com/gorilla/websocket` | WebSocket connections              |
| `golang.org/x/sync`          | errgroup for concurrent fetching     |

No dependency on `nats-server` or `nats.go`.

### Frontend

| Package                | Purpose                        |
|------------------------|--------------------------------|
| `react-router-dom`     | Client-side routing            |
| `@tanstack/react-table`| Data tables with pagination    |
| `react-force-graph-2d` | Topology graph visualization   |
| `zustand`              | State management               |
| `tailwindcss`          | Styling                        |
| `lucide-react`         | Icons                          |

## Build Output

The Vite build outputs to `internal/api/dist/`. The Go binary embeds this directory via `//go:embed dist/*`. The result is a single static binary with no external file dependencies (except the config file and data directory).
