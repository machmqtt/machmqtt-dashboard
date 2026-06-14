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
┌──────────────┐     HTTP polling     ┌───────────────────┐
│ NATS Server  │◄────────────────────►│  Go Backend       │
│ :8222 (mon)  │  /varz /connz /jsz   │  Collector        │
└──────────────┘                      │  ↓ Snapshot cache │
                                      │  ↓ SQLite metrics │
                                      │  ↓ WebSocket hub  │
                                      └────────┬──────────┘
                                               │ WS push
                                      ┌────────▼──────────┐
                                      │  React Frontend   │
                                      │  Zustand store    │
                                      └───────────────────┘
```

The backend polls each NATS server's HTTP monitoring endpoints on a configurable interval (default 30s). All dashboard users share the same cached snapshot — multiple people viewing the dashboard generates zero additional load on your NATS cluster.

Time-series metrics are stored in SQLite for trend charts (configurable retention, default 24h).

## Quick Start

### Docker Compose

Spin up a 3-node NATS cluster with the dashboard:

```bash
# Required: the dashboard refuses to start without a real session secret
export SESSION_SECRET=$(openssl rand -hex 32)
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` / `admin`.

### From Source

Prerequisites: Go 1.22+, Node.js 20+

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

Clusters are **not** configured in YAML — they're added and managed at runtime via the
admin UI (Cluster Management), including servers, TLS, MQTT bridge discovery, and the
optional NATS push-collection modes. A default admin user (`admin`/`admin`) is created on
first startup; you'll be required to change the password on first login.

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
go test ./internal/...
```

Integration tests run automatically when a NATS server is available on `localhost:4222`/`localhost:8222`. To start one:

```bash
docker run -d -p 4222:4222 -p 8222:8222 nats:latest -js -m 8222
```

## NATS Endpoints Used

The dashboard reads from these HTTP monitoring endpoints. No NATS client connection is required.

| Endpoint | Data | Poll Frequency |
|----------|------|----------------|
| `/varz` | Server stats, CPU, memory | Every cycle |
| `/routez` | Cluster routes | Every cycle |
| `/gatewayz` | Supercluster gateways | Every cycle |
| `/leafz` | Leaf node connections | Every cycle |
| `/healthz` | Server health | Every cycle |
| `/connz` | Client connections | Every 3rd cycle |
| `/subsz` | Subscription stats | Every 3rd cycle |
| `/jsz` | JetStream streams/consumers | Every 3rd cycle |
| `/accountz` | Account listing | Every 3rd cycle |
| `/accstatz` | Per-account message stats | Every 3rd cycle |

## Tech Stack

- **Backend**: Go, SQLite (WAL mode), WebSocket (gorilla/websocket), JWT auth
- **Frontend**: React 19, TypeScript, Zustand, TanStack Table, Recharts, Tailwind CSS, Vite
- **Deployment**: Single binary with embedded frontend, Docker, Docker Compose

## License

[AGPL-3.0](LICENSE) — Copyright (C) 2026 NoodleBit LLC
