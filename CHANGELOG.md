# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Distinct bridge readiness states** — a broker whose `/readyz` answers 503
  with a state body is no longer treated as unreachable: `draining`,
  `jetstream-degraded` and `not ready` are parsed, kept by discovery, counted as
  reachable, and rendered as amber badges (with a banner explaining that a
  JetStream-degraded broker still serves MQTT). New relay endpoint
  `GET /api/environments/{env}/mqtt/{bridge}/readyz`.
- **License status severity** — `tampered` and `expired` render red with a
  banner, `grace` amber, `valid` green.
- **Fleet freshness** — every fleet entry carries `last_seen` (push receipt,
  connz snapshot, or probe time); stale cards are flagged in the UI. Push
  bridges now report real in/out byte rates derived from pool-slot byte deltas.
- **Honest connection totals** — `/connz` reports the server-reported total and
  a `truncated` flag when the per-server fetch cap was hit; the Connections page
  says "showing first N of M" instead of presenting the cap as the total. The
  subscriptions detail path carries the same flag.
- **Full MachMQTT v1.2 metrics parity** — `MQTTMetrics` now mirrors the broker's
  metrics snapshot field-for-field on both ingestion paths. New on the collector:
  the license, I/O-reactor and NATS-connection-pool groups (including per-slot
  publish backlog and message counts) as nested objects; per-source cluster HMAC
  verification failures; raw histogram bucket arrays for all eight latency
  histograms; the `suback_rejected_by_reason` code map; the `qos2_sync_persist`
  histogram; and the new counters and gauges for peak connections, memory-budget
  rejections, hook panics/vetoes, `$SYS` tree publishes and spoof blocks, inbound
  byte volume, credential-expiry disconnects, mTLS identity fallbacks, WebSocket
  protocol violations, will/retain persistence failures, will and subscribe
  verification failures, lease revision regressions, cluster heartbeat publish
  failures, session-persist panics, OAuth2 token-cache evictions, and the
  credential-lockout tracker size.

### Changed
- The bridge `/metrics` parser now resolves Prometheus label escapes (`\\`, `\"`,
  `\n`), matches label keys whole (so `instance_id` no longer matches
  `source_instance_id`), accepts every exposition float form for counters
  (`1e+06`, `1.5`), and ignores an optional trailing sample timestamp. Unknown
  metric families are still ignored, unchanged.
- Drained bridges stay on the fleet as **Draining** (with their live session
  counts) until their metrics publishes stop, instead of vanishing the moment
  the drain begins.
- MQTT fleet trend charts aggregate one point per time bucket across bridges
  (sum of per-bridge bucket averages) instead of drawing every bridge's row on
  one line.
- Fleet listing cost no longer scales with viewers: the response body is cached
  per environment, and probes of configured-but-undiscovered bridges run in the
  background (single-flight, bounded timeout) so an unreachable bridge cannot
  stall the fleet page. A configured bridge whose broker reports a different
  instance name merges into the discovered entry instead of double-counting.

### Fixed
- A data race in the bridge cache: the fleet read path mutated cached metrics
  under a read lock while API handlers marshalled the same structs. Envelope
  fixups now happen once at ingest; cached messages are immutable.
- Link health for the metrics subscriber and the `$SYS` collector reported
  "connected" whenever a client existed, even while NATS was unreachable.
- NATS server rate computations clamp counter resets, so a server restart no
  longer writes large negative rates into charts and stored history.
- Subscription callbacks are panic-contained and the initial subscribe retries
  with backoff instead of dying permanently.

### Security
- Content-Security-Policy `connect-src` no longer allows WebSocket connections
  to arbitrary hosts; the data directory (SQLite with credential material) is
  created and re-secured as `0700`; requires Go 1.26.5 (crypto/tls fix for
  GO-2026-5856 — govulncheck reports zero reachable vulnerabilities).

## [1.0.0] - 2026-07-13

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
