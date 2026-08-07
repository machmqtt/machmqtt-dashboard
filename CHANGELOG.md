# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- **Filesystem paths are no longer settable through the cluster admin API**
  (CodeQL `go/path-injection`). `tls.ca_file`, `nats_conn.tls.ca_file` and
  `nats_conn.creds_file` name files on the dashboard host, and the resulting
  error was returned verbatim to the caller — so anyone holding the `admin` role
  (often an LDAP/OIDC-mapped identity with no shell on the host) could tell
  "permission denied" from "no such file" and enumerate the filesystem, or stall
  and OOM the process by naming a device file such as `/dev/zero`. These fields
  are now config-file-only: the stored value always wins and a request that tries
  to change one is rejected. Pin a custom CA over the API with the new inline
  `tls.ca_pem` instead. CA bundles must now be regular files of at most 1 MiB.
- **A CA bundle that contains no usable certificates is now an error.** The
  monitoring-endpoint fetcher discarded `AppendCertsFromPEM`'s result, so a
  malformed `ca_file` installed an empty trust pool and every TLS connection
  failed later with an opaque verification error instead of at config time.
- **`GET /metrics` is no longer anonymous.** The endpoint discloses environment
  names, collector endpoint names, configured auth provider names, and runtime
  internals. It now requires either the new `metrics_token` (sent by the scraper
  as `Authorization: Bearer <token>`) or an authenticated dashboard session.
  `/livez` and `/readyz` remain open for probes. **Breaking for existing
  Prometheus scrapes** — configure `metrics_token`/`metrics_token_file` and set
  `authorization.credentials_file` on the scrape job.
- **Removed the default `admin`/`admin` credential.** The initial administrator
  password must be supplied explicitly, and the account is flagged
  must-change-password on first login.
- **Login rate limiter no longer fails closed at its key bound.** Reaching the
  tracked-key cap previously rejected every unseen client, so anyone able to vary
  their source address could lock all users out of logging in. The table now
  evicts expired and least-recently-active entries instead, and never evicts a
  key that is currently blocked (which would have reset its budget). Evictions
  are exported as `nats_dashboard_authentication_rate_limiter_evictions_total`.
- **Upgraded `react-router` 7.18.2 → 8.3.0**, clearing GHSA-qwww-vcr4-c8h2 (high:
  RSC-mode CSRF bypass). The lockfile had been pinned inside the affected range.
  A transitive `brace-expansion` advisory in the lint toolchain was also cleared;
  `npm audit` now reports zero vulnerabilities.
- **Patched flagged Go dependencies** — `klauspost/compress` 1.18.6 → 1.18.7
  (GO-2026-5841) and `Azure/go-ntlmssp` 0.1.0 → 0.1.1 (GO-2026-5543). Neither is
  reachable from this codebase, but `go-ntlmssp` sits in the LDAP dependency
  path. One advisory remains outstanding with no upstream fix available:
  GO-2026-5932 in `golang.org/x/crypto`, which `govulncheck` confirms is not
  called by this module.

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
- Changing your password sent two `Set-Cookie` headers, one carrying a session
  invalidated by the password change itself. Only the fresh session is issued
  now, and a failure to re-issue reports 500 instead of silently logging the
  user out.
- Schema migrations ran through three overlapping paths: the versioned ledger,
  an unconditional users-schema pass on every boot, and inline DDL that
  duplicated migrations 2-4. All schema changes now live in the single numbered
  ledger, including the previously unversioned `clusters` table and the
  `mqtt_bridge_metrics` gauge columns.
- `/metrics` formatted its response to the network while holding the mutex every
  request handler needs, so a slow scraper stalled the server. Counters are now
  snapshotted under the lock and formatted after releasing it.
- The subscriptions single-flight fetch ran on the winning caller's request
  context, so one client disconnecting cancelled the shared fetch and cached the
  empty result for a full TTL. It now runs on a detached, separately bounded
  context.
- Auth middleware returned 401 for database failures, logging every user out
  during a transient outage; genuine "user not found" is still 401, other errors
  are 500 and logged.
- `NewMetricsWriter` took `any` and panicked at runtime on an unsupported
  source; the union is now enforced at compile time.
- The OIDC flow cookie hardcoded `Secure`, breaking the callback binding check
  on `http://` deployments; it is now derived from the configured public URL.
  The flow-expiry sweep is amortized rather than scanning every entry per login.
- `scripts/test-mutation-go.sh` defaulted its baseline to `HEAD`, so a clean
  checkout produced an empty diff and reported success without mutating
  anything. It now defaults to the merge base and fails on an empty diff.
- Frontend mutation testing no longer excludes `StringLiteral` mutants, which
  had been hiding role- and path-string mutations in the auth-critical files.
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
