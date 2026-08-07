# MachMQTT Dashboard

A real-time monitoring dashboard for [NATS](https://nats.io) clusters. Built with Go and React.

## Features

- **Cluster Overview** — server health, connection counts, message rates, and subscription totals with real-time WebSocket updates
- **Server Detail** — per-server CPU, memory, connections, message throughput, and time-series trend charts
- **Topology** — interactive force-graph visualization of servers, routes, gateways, and leaf nodes
- **Connections** — sortable/filterable table of all client connections with subscription detail drilldown
- **Subscriptions** — browse subscriptions by subject across all servers with account and server filtering
- **JetStream** — streams, consumers, and per-account resource usage with cluster deduplication
- **Accounts** — NATS account listing with drilldowns into connections, leaf nodes, and subscriptions per account
- **MQTT Bridge** — auto-discovery and monitoring of [MachMQTT](https://machmqtt.com) bridge instances with connection metrics
- **Multi-Environment** — monitor multiple NATS clusters from a single dashboard
- **Dark Mode** — system-aware dark/light theme

## Architecture

```
┌──────────────┐  HTTP :8222 (monitoring, default)   ┌───────────────────┐
│ NATS Server  │◄───────────────────────────────────►│  Go Backend       │
│ /varz /connz │  NATS :4222 (optional push)         │  Collector        │
│ /jsz ...     │◄───────────────────────────────────►│  ↓ Snapshot cache │
└──────────────┘                                     │  ↓ SQLite metrics │
                                                      │  ↓ WebSocket hub  │
                                                      └────────┬──────────┘
                                                               │ WS push
                                                      ┌────────▼──────────┐
                                                      │  React Frontend   │
                                                      │  Zustand store    │
                                                      └───────────────────┘
```

By default the backend polls each NATS server's HTTP monitoring endpoints on a configurable interval (default 30s), with no NATS client connection required. A cluster can optionally configure a NATS client connection (`nats_conn`) for push-based collection instead — `$SYS` events for continuous server stats and/or a subscription for MachMQTT bridge metrics — with HTTP polling as the automatic fallback if push collection stops producing data. All dashboard users share the same cached snapshot — multiple people viewing the dashboard generates zero additional load on your NATS cluster or MachMQTT bridges.

Time-series metrics are stored in SQLite for trend charts (configurable retention, default 24h).

## Quick Start

### Docker Compose

Spin up a 3-node NATS cluster with the dashboard:

```bash
# Required: the dashboard refuses to start without a real session secret
export SESSION_SECRET=$(openssl rand -hex 32)
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080) and log in as `admin` with the one-time password you exported. The first session must rotate it.

### From Source

Prerequisites: Go 1.26+, Node.js 22+

```bash
# Clone
git clone https://github.com/machmqtt/machmqtt-dashboard.git
cd machmqtt-dashboard

# Create config
cp config.example.yaml config.yaml
# Edit config.yaml with your NATS server URLs, and set a real session_secret
# (>= 32 chars). Generate one with: openssl rand -hex 32
# Or supply it via the SESSION_SECRET env var instead of editing the file.

# Build
cd ui && npm install && npx vite build && cd ..
go build -o bin/machmqtt-dashboard ./cmd/machmqtt-dashboard

# Run
./bin/machmqtt-dashboard -config config.yaml
```

### Docker

```bash
docker build -t machmqtt-dashboard .
docker run -p 8080:8080 \
  -e SESSION_SECRET=$(openssl rand -hex 32) \
  -v ./config.yaml:/etc/machmqtt-dashboard/config.yaml:ro machmqtt-dashboard
```

## Configuration

```yaml
listen: ":8080"
poll_interval: 30s              # HTTP monitoring poll interval (default 30s)
session_secret: "change-me"     # required, >= 32 chars; or set the SESSION_SECRET env var
data_dir: "./data"
metrics_retention: 24h          # how long time-series samples are kept (default 24h)
# secure_cookies: true          # set when serving over HTTPS
# trust_proxy_headers: true     # honor X-Forwarded-For (only behind a trusted reverse proxy)
```

Clusters can be declared in YAML under an `environments:` key to seed them automatically
on first startup — an environment whose name isn't already in the database is created;
one that already exists is left untouched, so runtime edits are never overwritten. This
is what the Docker Compose stack above relies on: `config.docker.yaml` ships an
`environments:` block declaring the 3-node cluster, so `docker compose up` polls all
three servers with no manual step. Clusters can also be added and managed entirely at
runtime via the admin UI (Cluster Management), including servers, TLS, MQTT bridge
discovery, and the optional NATS push-collection modes. A default admin user
(`admin`/`admin`) is created on first startup; you'll be required to change the password
on first login.

See [config.example.yaml](config.example.yaml) for the fully commented configuration.

## Development

Run the backend and frontend separately for hot-reload:

```bash
# Terminal 1: Backend (requires config.yaml)
go run ./cmd/machmqtt-dashboard -config config.yaml

# Terminal 2: Frontend (proxies API to backend)
cd ui && npx vite
```

The Vite dev server proxies `/api` requests to the Go backend on `:8080`.

### Testing

```bash
make test
```

Collector integration tests spin up an in-process NATS server (`internal/testutil/natstest`, built on the `nats-server/v2` test helpers) — no external NATS instance or Docker container is required.

## NATS Endpoints Used

By default the dashboard reads from each server's HTTP monitoring endpoints, and no NATS
client connection is required.

| Endpoint | Data | Poll Frequency |
|----------|------|----------------|
| `/varz` | Server stats, CPU, memory | Every cycle |
| `/routez` | Cluster routes | Every cycle |
| `/gatewayz` | Supercluster gateways | Every cycle |
| `/leafz` | Leaf node connections | Every cycle |
| `/healthz` | Server health | Every cycle |
| `/connz` | Client connections | Every cycle |
| `/subsz` | Subscription stats | Every 3rd (slow) cycle |
| `/jsz` | JetStream streams/consumers | Every 3rd (slow) cycle |
| `/accountz` | Account listing | Every 3rd (slow) cycle |

When a cluster configures `nats_conn`, the dashboard also opens a NATS client connection
(port 4222 by default) for push-based collection: `$SYS` events for continuous
per-server stats and/or a subscription for MachMQTT bridge metrics. HTTP polling remains
the automatic fallback if push collection stops producing data.

## Tech Stack

- **Backend**: Go, SQLite (WAL mode), WebSocket (gorilla/websocket), JWT auth, NATS client (nats.go) for optional push collection
- **Frontend**: React 19, TypeScript, Zustand, TanStack Table, Recharts, Tailwind CSS, Vite
- **Deployment**: Single binary with embedded frontend, Docker, Docker Compose

## License

[AGPL-3.0](LICENSE) — Copyright (C) 2026 NoodleBit LLC
