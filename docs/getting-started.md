# Getting Started

This guide covers building and running the MachMQTT Dashboard from source or via Docker.

## Prerequisites

### From Source

- **Go** 1.26+
- **Node.js** 22+
- **npm** (ships with Node.js)

### Docker

- **Docker** 20.10+
- **Docker Compose** v2

## Quick Start with Docker Compose

The fastest way to get a working dashboard with a 3-node NATS cluster:

```bash
# Clone and enter the project
cd machmqtt-dashboard

# Start the 3-node NATS cluster + dashboard
docker compose up -d

# Open the dashboard
open http://localhost:8080
```

Login with `admin` / `admin`.

The compose stack runs:
- 3 NATS servers (nats-1, nats-2, nats-3) with JetStream enabled, clustered
- The dashboard on port 8080, polling all three servers

No manual cluster-creation step is needed: `config.docker.yaml` (mounted into the
dashboard container) declares an `environments:` entry naming the three servers, which
the dashboard seeds into its database on first startup.

### Generating Test Traffic

Install the [NATS CLI](https://github.com/nats-io/natscli) and connect to the cluster:

```bash
# Publish messages
nats pub test.subject "hello" --count=1000

# Create a JetStream stream
nats stream add EVENTS --subjects="events.>" --defaults

# Publish to the stream
nats pub events.order "order-123" --count=100

# Subscribe (creates a connection visible in the dashboard)
nats sub "test.>"
```

## Building from Source

### Build Everything

```bash
make build
```

This runs `npm install` + `vite build` for the frontend, then compiles the Go binary with version info to `bin/machmqtt-dashboard`.

### Build Steps (Manual)

```bash
# 1. Build the frontend
cd ui
npm install
npx vite build   # outputs to ../internal/api/dist/
cd ..

# 2. Build the backend
go build -o bin/machmqtt-dashboard ./cmd/machmqtt-dashboard
```

The Go binary embeds the frontend build output. The resulting binary is fully self-contained.

### Run

```bash
# Copy and edit the config
cp config.example.yaml config.yaml
# Edit config.yaml — set session_secret and server URLs

# Run
./bin/machmqtt-dashboard -config config.yaml
```

### Development Mode

Run the backend and frontend dev server separately for hot reload:

```bash
# Terminal 1: Go backend (serves API on :8080)
make dev-backend

# Terminal 2: Vite dev server (serves UI on :5173, proxies /api to :8080)
make dev-frontend
```

Open `http://localhost:5173` during development. Vite proxies all `/api/*` requests to the Go backend.

## Docker Build

Build a standalone Docker image:

```bash
make docker-build
# or
docker build -t machmqtt-dashboard .
```

Run it:

```bash
docker run -p 8080:8080 \
  -v $(pwd)/config.yaml:/etc/machmqtt-dashboard/config.yaml:ro \
  -v dashboard-data:/data \
  machmqtt-dashboard
```

The Docker image:
- Uses a 3-stage build (Node.js -> Go -> Alpine 3.21)
- Produces a `CGO_ENABLED=0` static binary
- Runs as non-root user `app` (uid 1000)
- Expects config at `/etc/machmqtt-dashboard/config.yaml`
- Stores SQLite database in `/data`

## Running Tests

```bash
make test
```

This runs all Go unit tests with a 120-second timeout.

## CLI Flags

```
machmqtt-dashboard [flags]

Flags:
  -config string    Path to config file (default "config.yaml")
  -version          Print version and exit
  -example-config   Print a fully commented example config.yaml to stdout and exit
```

## Ports

| Service          | Port | Description              |
|------------------|------|--------------------------|
| Dashboard HTTP   | 8080 | Web UI + API + WebSocket |
| NATS Client      | 4222 | NATS client connections  |
| NATS Monitoring  | 8222 | NATS HTTP monitoring API |
| NATS Cluster     | 6222 | Inter-node routing       |

## Next Steps

- [Configuration Reference](configuration.md) for all config options
- [API Reference](api-reference.md) for REST and WebSocket endpoints
- [Architecture](architecture.md) for how the system works
