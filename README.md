# NATS Dashboard

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
┌─────────────┐     HTTP polling      ┌──────────────────┐
│ NATS Server  │◄────────────────────►│  Go Backend       │
│ :8222 (mon)  │  /varz /connz /jsz   │  Collector        │
└─────────────┘                       │  ↓ Snapshot cache  │
                                      │  ↓ SQLite metrics  │
                                      │  ↓ WebSocket hub   │
                                      └────────┬──────────┘
                                               │ WS push
                                      ┌────────▼──────────┐
                                      │  React Frontend    │
                                      │  Zustand store     │
                                      └───────────────────┘
```

The backend polls each NATS server's HTTP monitoring endpoints on a configurable interval (default 5s). All dashboard users share the same cached snapshot — multiple people viewing the dashboard generates zero additional load on your NATS cluster.

Time-series metrics are stored in SQLite for trend charts (configurable retention, default 24h).

## Quick Start

### Docker Compose

Spin up a 3-node NATS cluster with the dashboard:

```bash
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` / `admin`.

### From Source

Prerequisites: Go 1.22+, Node.js 20+

```bash
# Clone
git clone https://github.com/noodlebit/nats-dashboard.git
cd nats-dashboard

# Create config
cp config.example.yaml config.yaml
# Edit config.yaml with your NATS server URLs

# Build
cd ui && npm install && npx vite build && cd ..
go build -o bin/nats-dashboard ./cmd/nats-dashboard

# Run
./bin/nats-dashboard -config config.yaml
```

### Docker

```bash
docker build -t nats-dashboard .
docker run -p 8080:8080 -v ./config.yaml:/etc/nats-dashboard/config.yaml:ro nats-dashboard
```

## Configuration

```yaml
listen: ":8080"
poll_interval: 5s
session_secret: "change-me-to-a-random-string"
data_dir: "./data"

environments:
  - name: production
    servers:
      - url: "http://nats-1:8222"
      - url: "http://nats-2:8222"
      - url: "http://nats-3:8222"
    tls:
      ca_file: "/path/to/ca.pem"
```

A default admin user (`admin`/`admin`) is created on first startup. Change the password after first login.

See [config.example.yaml](config.example.yaml) for all options including MQTT bridge discovery and TLS configuration.

## Development

Run the backend and frontend separately for hot-reload:

```bash
# Terminal 1: Backend (requires config.yaml)
go run ./cmd/nats-dashboard -config config.yaml

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
