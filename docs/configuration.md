# Configuration Reference

The dashboard is configured via a YAML file, specified with the `-config` flag (default: `config.yaml`). Print a fully commented example with:

```bash
machmqtt-dashboard -example-config
```

## Full Example

```yaml
listen: ":8080"
poll_interval: 30s
session_secret: "change-me-to-a-random-32+-char-string"
data_dir: "./data"
metrics_retention: 24h
# secure_cookies: true        # set when serving over HTTPS
# trust_proxy_headers: false  # honor X-Forwarded-For (only behind a trusted proxy)

# Optional: seed clusters on first startup (see "Environments" below).
# environments:
#   - name: production
#     servers:
#       - url: "http://nats-1.internal:8222"
```

> **Clusters don't have to be configured in YAML.** The `environments:` key (see
> below) is optional and only *seeds* clusters on first startup — matched by name, so
> it never overwrites a cluster that already exists in the database. Clusters can also
> be added and managed entirely at runtime via the admin UI (Cluster Management) and
> persisted in the database, including server URLs, TLS, MQTT bridge discovery, and
> the optional NATS push-collection modes (MQTT metrics and `$SYS` server stats).

## Default Admin User

On startup, if no local administrator exists, the dashboard creates a local `admin` account using an explicitly supplied one-time bootstrap password. Supply it through `authentication.local.bootstrap_password_file` (preferred), `authentication.local.bootstrap_password`, or the `MACHMQTT_DASHBOARD_BOOTSTRAP_PASSWORD` environment variable. `NATS_DASHBOARD_BOOTSTRAP_PASSWORD` remains a deprecated compatibility alias.

- **Username:** `admin`
- **Password:** operator-supplied, at least 12 characters
- **Role:** `admin`

The account is flagged `must_change_password`, so the UI forces a password change on first login. The break-glass admin is only created when no local administrator exists; subsequent startups skip this step.

Admin users can create additional users via the User Management page in the UI or the admin API endpoints.

## Fields

| Field                 | Type     | Default    | Required | Description |
|-----------------------|----------|------------|----------|-------------|
| `listen`              | string   | `":8080"`  | No       | HTTP listen address (`host:port` or `:port`) |
| `poll_interval`       | duration | `30s`      | No       | How often to poll NATS monitoring endpoints (HTTP fallback mode) |
| `session_secret`      | string   | —          | **Yes**  | Key for signing JWT session tokens; must be ≥ 32 characters. Can also be supplied via the `SESSION_SECRET` env var, which overrides the file value. |
| `data_dir`            | string   | `"./data"` | No       | Directory for the SQLite database file |
| `metrics_retention`   | duration | `24h`      | No       | How long time-series samples are kept before the cleanup pass deletes them |
| `secure_cookies`      | bool     | `false`    | No       | Set the `Secure` flag on the session cookie (use when serving over HTTPS). When left `false`, the dashboard logs a startup warning that session cookies will be sent over plain HTTP. |
| `trust_proxy_headers` | bool     | `false`    | No       | Honor `X-Forwarded-For` when identifying the client IP for login rate limiting. Enable only behind a trusted reverse proxy — otherwise the header is spoofable. |
| `environments`        | list     | `[]`       | No       | Clusters to seed into the database on first startup. See [Environments](#environments) below. |

## Environments

The `environments` key declares clusters that should exist on startup. Each entry is
matched by `name`: one not already present in the database is created; one that already
exists is left completely untouched, so edits made later through the admin UI are never
overwritten by the config file. After seeding, every cluster (config-seeded or
UI-created) is managed the same way — via the admin UI/API — and stored in the
database. This is what lets `docker compose up` bring up a fully working, already-polling
dashboard with no manual cluster-creation step (see [config.docker.yaml](../config.docker.yaml)).

```yaml
environments:
  - name: production
    servers:
      - url: "http://nats-1.internal:8222"
      - url: "http://nats-2.internal:8222"
    admin_token: ""            # optional: shared MachMQTT bridge admin bearer token
    mqtt_bridges:               # optional: statically configured bridges
      - name: "edge-broker-1"
        url: "http://bridge-1.internal:8080"
        bearer_token: ""
    mqtt_discovery:             # optional: tune bridge auto-discovery
      enabled: true
      admin_ports: [8080]
      trusted_hosts: []
    tls:                        # optional: TLS for the HTTP monitoring endpoints above
      ca_file: ""
      insecure: false
    nats_conn:                  # optional: NATS client connection for push collection
      urls: ["nats://nats-1.internal:4222"]
      username: ""
      password: ""
      token: ""
      nkey: ""
      creds_file: ""
      subject_prefix: "$MQTT5"
      sys_collection: false
      tls:
        ca_file: ""
        insecure: false
```

Key fields:

| Field | Description |
|-------|-------------|
| `name` | Required. The cluster's display name; also the seeding match key. |
| `servers` | List of `{url}` NATS HTTP monitoring endpoints (e.g. `http://host:8222`). |
| `admin_token` | Default MachMQTT bridge admin bearer token for this environment, used for every bridge (auto-discovered or configured) that doesn't set its own `bearer_token`. |
| `mqtt_bridges` | Statically configured bridges, in addition to auto-discovery. Each has `name`, `url`, and an optional per-bridge `bearer_token` overriding the environment default. |
| `mqtt_discovery.enabled` | Whether to auto-discover MachMQTT bridges. Defaults to `true`. |
| `mqtt_discovery.admin_ports` | Ports to probe on a discovered bridge's host. Defaults to `[8080]`. |
| `mqtt_discovery.trusted_hosts` | Extends the set of hosts the environment's `admin_token` may be sent to during auto-discovery. Loopback and any host already named in this environment's `servers`/`mqtt_bridges`/`nats_conn` URLs are always trusted. A discovered bridge whose host is **not** trusted is still probed, but **without** the admin token — so a rogue address that merely announces itself as a bridge can never capture the shared secret. |
| `tls` | `ca_file` (custom CA for the HTTP monitoring endpoints) and `insecure` (skip TLS verification). |
| `nats_conn.urls` | One or more `nats://` seed server URLs for push-based collection. Omit `nats_conn` entirely for HTTP-only polling. |
| `nats_conn` auth | Set exactly one of `username`/`password`, `token`, `nkey`, or `creds_file`. |
| `nats_conn.subject_prefix` | MachMQTT subject namespace; must match the prefix MachMQTT is configured with. Defaults to `$MQTT5`. |
| `nats_conn.sys_collection` | Enables `$SYS`-based server collection (replaces HTTP polling for server stats; requires system-account credentials). Defaults to `false`. |
| `nats_conn.tls` | TLS options for the NATS client connection itself. |

## Polling Behavior

When a cluster has no push-collection mode configured, the collector polls its NATS HTTP monitoring endpoints using a two-tier strategy:

- **Fast poll** (every `poll_interval`): `/varz`, `/routez`, `/gatewayz`, `/leafz`, `/healthz`, `/connz` — needed every cycle for topology, overview, and the connections page
- **Slow poll** (every 3× `poll_interval`): `/subsz`, `/jsz`, `/accountz` — heavier endpoints that return more data

With the default 30s interval, fast data updates every 30 seconds and slow data every 90 seconds. Each poll fetches all servers in a cluster concurrently.

Clusters configured with `$SYS` collection instead subscribe to `$SYS.SERVER.*.STATSZ` and use `$SYS.REQ.SERVER.PING.*` fan-in; HTTP polling remains the automatic fallback.

## Session Tokens

Sessions use HMAC-SHA256 signed JWTs stored in an `httpOnly`, `SameSite=Strict` cookie. Tokens expire after 24 hours.

The `session_secret` must be:
- At least 32 characters (the shipped placeholder is intentionally too short, so the app refuses to start until you set a real one)
- Kept secret and consistent across restarts (changing it invalidates all sessions)

Generate a secret:

```bash
openssl rand -hex 32
```

## Data Directory

The `data_dir` contains a single SQLite database file (`dashboard.db`) that stores user accounts, cluster configuration, discovered MQTT bridges, time-series metrics, and topology layout. It is created automatically on first run with WAL journaling and incremental `auto_vacuum` enabled (freed space is reclaimed after retention pruning; a database created before 1.0 is converted on first open). The directory is created if it doesn't exist.

Cluster secrets (`admin_token`, `nats_conn` credentials) are stored in this database as
plaintext columns, redacted only when served back through the API — see
[Security Considerations](deployment.md#security-considerations) for how to protect this
directory.

One dashboard process per data directory is supported. OIDC state is one-time, browser-bound, retained in bounded process memory for ten minutes, and is not shared between replicas. Do not route an OIDC callback to another replica.

Startup acquires a non-blocking lock in the data directory and fails if another process owns it. OIDC flow count/evictions, rate-limiter key count/rejections/evictions, and SQLite WAL size are exported on `/metrics`.

### `metrics_token` / `metrics_token_file`

Bearer token that authorizes Prometheus to scrape `/metrics`. Minimum 16 characters. `metrics_token_file` reads the value from a file so the secret never appears in the config. When neither is set, `/metrics` requires a dashboard session instead — it is never anonymous, because it discloses environment names, collector endpoints, and configured auth provider names.

## Environment Variables

`SESSION_SECRET` overrides the `session_secret` config value when set, so the secret can be injected without writing it to disk (Docker, CI, secret managers). All other configuration is file-based.

## Validation

The config loader validates that `session_secret` is set (via the file or `SESSION_SECRET`) and is at least 32 characters. Cluster-level validation (server URLs, TLS readability) happens when a cluster is added or updated through the admin UI/API.
