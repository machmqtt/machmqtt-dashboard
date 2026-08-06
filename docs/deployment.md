# Deployment

## Docker (Recommended)

### Build the Image

```bash
docker build -t machmqtt-dashboard .

# With version tag
docker build --build-arg VERSION=v1.0.0 -t machmqtt-dashboard:v1.0.0 .
```

### Run

```bash
docker run -d \
  --name machmqtt-dashboard \
  -p 8080:8080 \
  -v /path/to/config.yaml:/etc/machmqtt-dashboard/config.yaml:ro \
  -v dashboard-data:/data \
  machmqtt-dashboard
```

The container:
- Runs as non-root user `app` (uid 1000)
- Expects config at `/etc/machmqtt-dashboard/config.yaml`
- Stores the SQLite database in `/data`
- Listens on the port defined in the config (`listen` field)

### Docker Compose

For a complete local development stack with a 3-node NATS cluster:

```bash
docker compose up -d
```

This starts:
- `nats-1`, `nats-2`, `nats-3` — Clustered NATS servers with JetStream
- `dashboard` — The dashboard, configured to poll all three servers

No manual cluster setup is required: the mounted `config.docker.yaml` declares an
`environments:` entry naming the three servers, which the dashboard seeds into its
database (idempotently, on every startup) the first time it doesn't already exist.

Ports:
- `8080` — Dashboard UI
- `4222-4224` — NATS client connections
- `8222-8224` — NATS monitoring endpoints

## Binary Deployment

### Build

```bash
make build
# Produces: bin/machmqtt-dashboard
```

The binary is statically linked (`CGO_ENABLED=0`) and self-contained. Copy it and the config file to your server.

### Systemd Service

Create `/etc/systemd/system/machmqtt-dashboard.service`:

```ini
[Unit]
Description=MachMQTT Dashboard
After=network.target

[Service]
Type=simple
User=machmqtt-dashboard
ExecStart=/usr/local/bin/machmqtt-dashboard -config /etc/machmqtt-dashboard/config.yaml
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
# Copy files
sudo cp bin/machmqtt-dashboard /usr/local/bin/
sudo mkdir -p /etc/machmqtt-dashboard /var/lib/machmqtt-dashboard
sudo cp config.yaml /etc/machmqtt-dashboard/

# Create user
sudo useradd -r -s /usr/sbin/nologin machmqtt-dashboard
sudo chown -R machmqtt-dashboard: /var/lib/machmqtt-dashboard

# Set data_dir in config.yaml to /var/lib/machmqtt-dashboard

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable machmqtt-dashboard
sudo systemctl start machmqtt-dashboard
```

## Kubernetes

Example deployment manifest:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: machmqtt-dashboard
spec:
  replicas: 1
  selector:
    matchLabels:
      app: machmqtt-dashboard
  template:
    metadata:
      labels:
        app: machmqtt-dashboard
    spec:
      containers:
        - name: dashboard
          image: machmqtt-dashboard:latest
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/machmqtt-dashboard
              readOnly: true
            - name: data
              mountPath: /data
      volumes:
        - name: config
          configMap:
            name: machmqtt-dashboard-config
        - name: data
          persistentVolumeClaim:
            claimName: machmqtt-dashboard-data
---
apiVersion: v1
kind: Service
metadata:
  name: machmqtt-dashboard
spec:
  selector:
    app: machmqtt-dashboard
  ports:
    - port: 8080
      targetPort: 8080
```

Create the ConfigMap from your config file:

```bash
kubectl create configmap machmqtt-dashboard-config --from-file=config.yaml
```

## Reverse Proxy

### Nginx

```nginx
server {
    listen 443 ssl;
    server_name dashboard.example.com;

    ssl_certificate     /etc/ssl/certs/dashboard.pem;
    ssl_certificate_key /etc/ssl/private/dashboard.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /api/ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 86400;
    }
}
```

The WebSocket endpoint (`/api/ws`) requires the `Upgrade` and `Connection` headers to be forwarded.

## Network Requirements

By default the dashboard needs HTTP access to the NATS monitoring port (default 8222) on
each configured server, and does not need the NATS client port. Ensure firewall rules
allow:

- Dashboard -> NATS servers: TCP port 8222 (or custom monitoring port)
- Browsers -> Dashboard: TCP port 8080 (or custom listen port)

If a cluster configures `nats_conn` for push-based collection (`$SYS` server stats
and/or MachMQTT bridge metrics), the dashboard also opens a NATS client connection to
that cluster — allow TCP port 4222 (or custom client port) in that case. HTTP polling
of the monitoring port remains available as the automatic fallback if push collection
stops producing data, so it's reasonable to keep both open.

## Security Considerations

- Change the `session_secret` from the default value
- Change the default admin password after first login
- Use TLS termination via a reverse proxy for production, and set `secure_cookies: true`
  in the config when serving over HTTPS — the dashboard logs a startup warning when it's
  left `false`
- If NATS monitoring endpoints use HTTPS, configure the `tls` section in the environment config
- User passwords are bcrypt-hashed, but the `clusters` table stores each cluster's
  `admin_token` and `nats_conn` (which may hold NATS passwords, tokens, nkeys, or
  credentials) as **plaintext** columns — these values are only redacted when served
  back through the API, not at rest. Restrict filesystem access to `data_dir` (and back
  up the database file) accordingly.
- Session cookies are `httpOnly` and `SameSite=Strict`

Sessions have a 24-hour absolute lifetime and no sliding idle timeout. Password changes, local account deletion/disable, and role changes invalidate outstanding sessions through a server-side version. Rotating `session_secret` invalidates every session. Logout clears the browser cookie but does not maintain a per-token denylist; a copied token remains usable until expiry or one of those revocation events, so use TLS and rotate the affected account or signing secret after suspected theft.

The ordered password login limiter and the dedicated local recovery limiter intentionally have separate bounded budgets. This preserves break-glass access during an external-provider attack or outage without allowing alternate external endpoints to multiply the external attempt budget. Only configured `trusted_proxy_cidrs` may supply client forwarding headers. Limiter and OIDC flow state are process-local, reinforcing the single-instance deployment boundary.

## Health, metrics, and recovery

- `GET /livez` reports process liveness and has no external dependency.
- `GET /readyz` verifies the process is accepting work and SQLite is reachable. It becomes unavailable during graceful shutdown; stale NATS observations are treated as degraded monitoring, not process unreadiness.
- `GET /metrics` exposes Prometheus text metrics for HTTP requests, authentication outcomes, the persistence queue, SQLite pool usage, and WebSocket clients/drops. Labels are bounded and never contain usernames, subjects, tokens, client IDs, or raw URLs.
- Every response includes `X-Request-ID`; structured request logs include normalized route, status, duration, response size, and client class.

Only one dashboard process may use a data directory. Before offline backup or restore, stop the process. Online backups use SQLite `VACUUM INTO`; verify the result with `PRAGMA quick_check`. Restore by replacing `dashboard.db` while stopped, preserving ownership and permissions, then start and verify `/readyz`. If integrity checking fails, retain the database and WAL files for investigation and restore the last verified backup rather than attempting an in-place downgrade.

Metrics retention is configured by `metrics_retention` (default `24h`; supported `1h` through `8760h`). Size the volume from the polling interval, server/bridge count, and retention window.

See [Performance and capacity](performance.md) for dataset shapes, query limits, sizing guidance, and the repeatable benchmark command.
