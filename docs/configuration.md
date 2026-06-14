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
```

> **Clusters are not configured in YAML.** They are added and managed at runtime
> via the admin UI (Cluster Management) and persisted in the database — including
> server URLs, TLS, MQTT bridge discovery, and the optional NATS push-collection
> modes (MQTT metrics and `$SYS` server stats). The YAML file only holds
> process-level settings.

## Default Admin User

On first startup, if the user database is empty, a default administrator account is created automatically:

- **Username:** `admin`
- **Password:** `admin`
- **Role:** `admin`

The account is flagged `must_change_password`, so the UI forces a password change on first login. The default admin is only created when no users exist; subsequent startups skip this step.

Admin users can create additional users via the User Management page in the UI or the admin API endpoints.

## Fields

| Field                 | Type     | Default    | Required | Description |
|-----------------------|----------|------------|----------|-------------|
| `listen`              | string   | `":8080"`  | No       | HTTP listen address (`host:port` or `:port`) |
| `poll_interval`       | duration | `30s`      | No       | How often to poll NATS monitoring endpoints (HTTP fallback mode) |
| `session_secret`      | string   | —          | **Yes**  | Key for signing JWT session tokens; must be ≥ 32 characters. Can also be supplied via the `SESSION_SECRET` env var, which overrides the file value. |
| `data_dir`            | string   | `"./data"` | No       | Directory for the SQLite database file |
| `metrics_retention`   | duration | `24h`      | No       | How long time-series samples are kept before the cleanup pass deletes them |
| `secure_cookies`      | bool     | `false`    | No       | Set the `Secure` flag on the session cookie (use when serving over HTTPS) |
| `trust_proxy_headers` | bool     | `false`    | No       | Honor `X-Forwarded-For` when identifying the client IP for login rate limiting. Enable only behind a trusted reverse proxy — otherwise the header is spoofable. |

## Polling Behavior

When a cluster has no push-collection mode configured, the collector polls its NATS HTTP monitoring endpoints using a two-tier strategy:

- **Fast poll** (every `poll_interval`): `/varz`, `/routez`, `/gatewayz`, `/leafz`, `/healthz` — lightweight endpoints needed for topology and overview
- **Slow poll** (every 3× `poll_interval`): `/connz`, `/subsz`, `/jsz`, `/accountz` — heavier endpoints that return more data

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

The `data_dir` contains a single SQLite database file (`dashboard.db`) that stores user accounts, cluster configuration, discovered MQTT bridges, time-series metrics, and topology layout. It is created automatically on first run with WAL journaling enabled. The directory is created if it doesn't exist.

## Environment Variables

`SESSION_SECRET` overrides the `session_secret` config value when set, so the secret can be injected without writing it to disk (Docker, CI, secret managers). All other configuration is file-based.

## Validation

The config loader validates that `session_secret` is set (via the file or `SESSION_SECRET`) and is at least 32 characters. Cluster-level validation (server URLs, TLS readability) happens when a cluster is added or updated through the admin UI/API.
</content>
