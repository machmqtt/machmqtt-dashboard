# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0]

First stable release. Read-only monitoring for NATS clusters and MachMQTT
bridges, with a Go backend, an embedded React UI, SQLite-backed time series, and
live updates over WebSocket.

### Added
- **NATS monitoring** — cluster overview, per-server `varz`, connections
  (`connz`), routes, gateways, leaf nodes, subscriptions (`subsz`), JetStream
  (`jsz`), and accounts (`accountz`), plus a force-graph topology view with
  persisted node/camera positions.
- **`$SYS` collection with permanent HTTP-poll fallback** — when a cluster is
  configured with `nats_conn` + `sys_collection`, server stats are collected
  from `$SYS` events (with a request/reply bootstrap so the first poll is never
  blank); collection automatically falls back to the HTTP monitoring endpoints
  when `$SYS` is unavailable and resumes when events return.
- **MachMQTT bridge monitoring** — fleet overview, per-bridge connections,
  NATS/pool/license diagnostics, and time-series metrics, sourced from the
  bridge's NATS metrics push (`<prefix>.metrics.>`) with connz-scan HTTP
  discovery as a fallback while push is warming up or unavailable.
- **MachMQTT admin actions** (admin role) — kick client(s), drain/undrain, and
  reload, gated by an explicit action allowlist and audited.
- **Cluster management** — create/update/delete monitored clusters from the
  admin UI; secrets (bearer tokens, NATS credentials) are never returned in API
  responses.
- **Config-driven cluster seeding** — an `environments:` block in the config
  file is idempotently seeded into the database on startup (matched by name;
  existing clusters are never overwritten), so `docker compose up` polls the
  configured servers with no manual step.
- **Time-series metrics** — env/server/bridge history in SQLite with configurable
  retention, served to trend charts with server-side downsampling.
- **Authentication** — session-cookie auth (`HttpOnly`, `SameSite=Strict`),
  bcrypt password storage, a per-IP login rate limiter, and a forced
  password-change flow for the bootstrap admin.

### Security
- Sessions are revocable: JWTs carry a token version that is re-checked against
  the database on every request, so logout, password change, and account
  deletion invalidate outstanding sessions, and role changes take effect
  immediately.
- The forced password change for the bootstrap `admin` account is enforced
  server-side (not only in the UI).
- Auto-discovery only sends a cluster's admin token to loopback, configured, or
  operator-listed (`mqtt_discovery.trusted_hosts`) addresses — never to an
  arbitrary discovered host.
- NATS connection URLs are credential-redacted before logging; cluster and NATS
  secrets are stored at rest in the local database and should be protected
  accordingly (see the deployment guide).
- Login timing is equalized against username enumeration, per-account lockout
  complements the per-IP limiter, a minimum password length is enforced, and the
  app warns at startup when `secure_cookies` is disabled.

### Reliability & performance
- Every collector goroutine recovers from panics, so a fault in one cluster's
  collection cannot crash the process; goroutines are joined on shutdown.
- Bridge diagnostic reads are served from the cached NATS-push snapshot before
  falling back to a live fetch, with a short-lived cache for the remaining live
  reads, so dashboard viewers don't amplify load on monitored bridges.
- Retention pruning runs off the metrics-ingestion path, per-server subscription
  fetches run concurrently, time-series queries are point-bounded, and SQLite
  uses `synchronous=normal` with a bounded connection pool and incremental
  `auto_vacuum` (freed space is reclaimed after retention pruning).
- A WebSocket client that stays backed up (a stalled or backgrounded viewer) is
  force-closed after a sustained run of dropped messages so the browser
  reconnects and resyncs, rather than showing permanently-frozen data.
