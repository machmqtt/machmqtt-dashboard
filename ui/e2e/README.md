# End-to-end UI tests (Playwright)

These specs drive a real browser against an **already-running dashboard** — the
shipped `:8090` binary serving the embedded UI bundle, not the vite dev server —
so they exercise the Go server + embedded assets exactly as deployed.

## What's covered

| Spec | Asserts |
| --- | --- |
| `01-overview` | Overview loads; server table renders; long server name truncates with a tooltip |
| `02-nav-pages` | Every sidebar page routes and shows its heading; admin pages reachable |
| `03-subscriptions` | Per-server summary table renders (not the empty state); cache-hit shows as a 0–100% |
| `04-cluster-management` | Full CRUD on a throwaway cluster (create → edit → delete), API-guaranteed cleanup |
| `05-mqtt-fleet-detail` | Exactly one fleet card per bridge (merge guard); every detail tab loads with no error banner |
| `06-mqtt-connections` | Per-bridge connections list clients, or shows the "not available" reason |
| `07-fleet-refresh` | MachMQTT Fleet page refreshes its bridge cards in place on each poll, without collapsing the page or resetting scroll position |
| `99-mqtt-admin-actions` | **Destructive** — fires drain → undrain → reload → kick-all; force-undrains afterward |

Specs run serially (`workers: 1`) in filename order; the destructive spec is last.

## Running

```bash
cd ui
npm run test:e2e            # headless
npm run test:e2e:headed     # watch it drive the browser
npm run test:e2e:report     # open the last HTML report
```

## Stack prerequisites

The suite assumes a running stack with MachMQTT admin features enabled. The
defaults match the local dev stack (env `local-dev`, bridge `edge-broker-1`), but
nothing is hardcoded — the env and bridge are discovered from the live API.

1. **NATS + MachMQTT + dashboard running**, dashboard on `http://127.0.0.1:8090`.
2. **MachMQTT admin endpoints enabled** in `machmqtt.yaml` (otherwise the admin
   actions 403 and the License/Config/Cluster tabs stay "not configured"):
   ```yaml
   admin:
     addr: "127.0.0.1:8081"
     allow_kick_endpoint: true
     allow_drain_endpoint: true
     allow_reload_endpoint: true
     clients_snapshot_interval: 3s   # enables /connz (the per-client list)
   ```
3. **Bridge admin URL configured** on the dashboard environment, so the detail
   page reaches the admin API instead of falling back to push metrics:
   ```bash
   curl -b <admin-cookie> -X PUT http://127.0.0.1:8090/api/admin/clusters/<envId> \
     -H 'Content-Type: application/json' \
     -d '{"name":"local-dev","servers":[{"url":"http://127.0.0.1:8222"}],
          "mqtt_bridges":[{"name":"edge-broker-1","url":"http://127.0.0.1:8081"}],
          "nats_conn":{"urls":["nats://127.0.0.1:4222"],"subject_prefix":"$MQTT5"}}'
   ```
   **Required for `05-mqtt-fleet-detail`** as written: it asserts the live License
   (`Tier`) and Config (`Running Configuration`) tabs, which only render with the
   admin URL. Without it those tabs show the "admin endpoint not configured" reason
   and the spec fails. The push-only fallback path (Metrics/Pool/NATS from the push
   snapshot; License/Config/Cluster returning the reason) is pinned separately by
   the Go test `TestMQTTPushFallback` in `internal/api`, not by this UI suite.
4. **A live MQTT workload** (e.g. the traffic generator) so the connections page
   has clients to list. Without it, `06` falls back to asserting the reason banner.

## Authentication

The suite logs in as a dedicated admin (`e2e-admin`) so it never depends on the
real admin password (which may have been rotated by the forced first-login
change). `global-setup`:

- logs in as `e2e-admin` and saves the session as `storageState`; if that user
  doesn't exist yet and `E2E_BOOTSTRAP_USER`/`E2E_BOOTSTRAP_PASS` (an existing
  admin) are set, it creates `e2e-admin` first (idempotent — a re-run is fine).

If neither works, create the user once:

```bash
curl -b <admin-cookie> -X POST http://127.0.0.1:8090/api/admin/users \
  -H 'Content-Type: application/json' \
  -d '{"username":"e2e-admin","password":"E2ePlaywright!2026","role":"admin"}'
```

## Configuration (env vars)

| Var | Default | Purpose |
| --- | --- | --- |
| `E2E_BASE_URL` | `http://127.0.0.1:8090` | Dashboard URL |
| `E2E_ADMIN_USER` / `E2E_ADMIN_PASS` | `e2e-admin` / `E2ePlaywright!2026` | Test login |
| `E2E_BOOTSTRAP_USER` / `E2E_BOOTSTRAP_PASS` | — | Existing admin used to create the test user |
| `E2E_ENV_NAME` | `local-dev` | Which environment to target (falls back to the first) |
