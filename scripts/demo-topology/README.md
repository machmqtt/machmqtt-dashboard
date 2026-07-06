# Demo topology

A self-contained NATS leaf/cluster topology with a MachMQTT broker on each edge,
plus spiky traffic — for dashboard screenshots and end-to-end verification.

```
                 ┌─────── hub cluster (routes) ───────┐
                 │   hub-1 ── hub-2 ── hub-3           │   (JetStream off — pure routing)
                 └─────┬────────┬────────┬─────────────┘
                  leaf │   leaf │   leaf │
                     edge-1   edge-2   edge-3            (JetStream on, domain edge1/2/3)
                       │        │        │
                 edge-broker-1  -2       -3              (MachMQTT, one per edge)
                       │        │        │
                  MQTT clients (publishers + subscribers)
```

- **Hub**: 3-node NATS cluster, routing only (no JetStream).
- **Edges**: 3 leaf nodes, each with its own JetStream domain, each running a
  MachMQTT broker that uses the local JetStream.
- **Traffic**:
  - per-edge **MQTT** publishers/subscribers (spiky) → lights up each bridge and
    edge node, and the message-rate charts;
  - cross-edge **core-NATS** flow → routes edge→hub→edge, lighting the leaf and
    route links in the topology graph (messages moving "back and forth").

The dashboard polls every 2s and the brokers emit metrics every 2s, so bursts
show up as spikes instead of being averaged flat.

## Run

```bash
# dev license: export it, or drop the token in scripts/demo-topology/.license
export MACHMQTT_LICENSE_KEY="<dev token>"

./topology.sh up        # build + launch + configure the dashboard + start traffic
./topology.sh status    # what's running
./topology.sh shots     # capture PNGs of the key pages to .run/shots/
./topology.sh down      # stop everything (logs kept under .run/logs)
./topology.sh logs      # tail dashboard + a broker log
```

`shots` reuses the Playwright/chromium the e2e suite installs (`npm install` in
`ui/` first). Let traffic run a couple of minutes before capturing so the trend
charts have spiky history.

Then open **http://127.0.0.1:8095** (login `admin` / `demopassword`). Good
screenshot targets:

| Page | URL | Shows |
| --- | --- | --- |
| Topology | `/topology` | hub cluster + leaf edges + MQTT bridges, live link flow |
| Fleet | `/mqtt` | 3 MachMQTT bridges with live metrics |
| Overview | `/` | 6 servers, spiky message-rate charts |
| Bridge detail | `/mqtt/edge-broker-1/detail` | per-broker metrics, pool, NATS, license |

## Requirements / overrides

| Env | Default | Purpose |
| --- | --- | --- |
| `NATS_BIN` | `nats-server` on PATH, else `~/go/bin/nats-server` | NATS server binary |
| `MACHMQTT_BIN` | `../../../machmqtt/machmqtt` (sibling repo) | MachMQTT binary (build with the dev key) |
| `MACHMQTT_LICENSE_KEY` | — (or `.license` file) | dev license token; **never commit it** |

All runtime state lives under `.run/` (gitignored). `up` wipes and recreates it,
so each run is a clean stack. Ports are in the 31000–38000 range to avoid
clashing with a default local NATS/MachMQTT/dashboard stack.
