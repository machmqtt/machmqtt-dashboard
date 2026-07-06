#!/usr/bin/env bash
#
# Demo topology for the dashboard: a NATS hub cluster (3 nodes) + 3 leaf "edge"
# nodes, each running a MachMQTT broker, plus spiky MQTT and cross-edge traffic.
# Stands up a self-contained stack you can point a browser at for screenshots or
# to exercise the dashboard end to end.
#
#   ./topology.sh up        # build + launch everything, configure the dashboard
#   ./topology.sh status    # show what's running and health
#   ./topology.sh down      # stop everything, leave logs
#   ./topology.sh logs      # tail the dashboard + a broker log
#
# Requirements (override via env):
#   NATS_BIN              nats-server binary           (default: PATH / ~/go/bin)
#   MACHMQTT_BIN          machmqtt binary              (default: ../../../machmqtt/machmqtt)
#   MACHMQTT_LICENSE_KEY  dev license token (env), or a `.license` file beside this script
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RUN_DIR="$SCRIPT_DIR/.run"
PIDS_FILE="$RUN_DIR/pids"

NATS_BIN="${NATS_BIN:-$(command -v nats-server || echo "$HOME/go/bin/nats-server")}"
MACHMQTT_BIN="${MACHMQTT_BIN:-$REPO_ROOT/../machmqtt/machmqtt}"
DASH_PORT=8095
DASH_USER="admin"
DASH_PASS="demopassword"   # the demo login (set after first-run admin/admin)

# ── Port map (high ports to avoid clashing with a default local stack) ──
HUB_NAMES=(hub-1 hub-2 hub-3)
HUB_CLIENT=(34221 34222 34223)
HUB_MON=(38221 38222 38223)
HUB_CLUSTER=(36221 36222 36223)
HUB_LEAF=(37221 37222 37223)

EDGE_NAMES=(edge-1 edge-2 edge-3)
EDGE_CLIENT=(34231 34232 34233)
EDGE_MON=(38231 38232 38233)
EDGE_DOMAIN=(edge1 edge2 edge3)

BROKER_NAMES=(edge-broker-1 edge-broker-2 edge-broker-3)
BROKER_MQTT=(31883 31884 31885)
BROKER_ADMIN=(38181 38182 38183)

CLUSTER_SECRET="demo-fleet-shared-secret-0000000000000000000000000000000000000000"
# Admin bearer token for the brokers' admin API. Current MachMQTT requires a
# token whenever the destructive kick/drain/reload endpoints are enabled, so the
# broker config and the dashboard's cluster config below share this value. Demo
# only (loopback); never a real secret.
BROKER_ADMIN_TOKEN="demo-admin-token-000000000000000000000000"

log()  { printf '\033[1;36m[topology]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[topology]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[topology]\033[0m %s\n' "$*" >&2; exit 1; }

record_pid() { echo "$1 $2" >> "$PIDS_FILE"; }

wait_http() { # url, label, timeout_s
  local url="$1" label="$2" timeout="${3:-20}" i=0
  until curl -s -o /dev/null "$url" 2>/dev/null; do
    i=$((i+1)); [ "$i" -ge "$((timeout*2))" ] && die "timeout waiting for $label ($url)"
    sleep 0.5
  done
}

resolve_license() {
  if [ -n "${MACHMQTT_LICENSE_KEY:-}" ]; then return; fi
  if [ -f "$SCRIPT_DIR/.license" ]; then
    MACHMQTT_LICENSE_KEY="$(cat "$SCRIPT_DIR/.license")"; export MACHMQTT_LICENSE_KEY; return
  fi
  die "no MachMQTT license: set MACHMQTT_LICENSE_KEY or create $SCRIPT_DIR/.license (dev token; never commit it)"
}

# ─────────────────────────────────────────────────────────────────────────────
cmd_up() {
  [ -x "$NATS_BIN" ] || die "nats-server not found at $NATS_BIN (set NATS_BIN)"
  [ -x "$MACHMQTT_BIN" ] || die "machmqtt not found at $MACHMQTT_BIN (set MACHMQTT_BIN)"
  resolve_license

  cmd_down >/dev/null 2>&1 || true
  rm -rf "$RUN_DIR"; mkdir -p "$RUN_DIR/js" "$RUN_DIR/conf" "$RUN_DIR/logs" "$RUN_DIR/dash-data"
  : > "$PIDS_FILE"

  log "building dashboard + loadgen…"
  ( cd "$REPO_ROOT" && go build -o "$RUN_DIR/dashboard" ./cmd/machmqtt-dashboard )
  ( cd "$SCRIPT_DIR/loadgen" && go build -o "$RUN_DIR/loadgen" . )

  # ── Hub cluster (JS disabled — a pure routing cluster) ──
  local routes=""
  for c in "${HUB_CLUSTER[@]}"; do routes="${routes:+$routes, }nats-route://127.0.0.1:$c"; done
  for i in 0 1 2; do
    local f="$RUN_DIR/conf/${HUB_NAMES[$i]}.conf"
    cat > "$f" <<EOF
server_name: ${HUB_NAMES[$i]}
listen: 127.0.0.1:${HUB_CLIENT[$i]}
http: 127.0.0.1:${HUB_MON[$i]}
cluster {
  name: hub
  listen: 127.0.0.1:${HUB_CLUSTER[$i]}
  routes: [ ${routes} ]
}
leafnodes { listen: 127.0.0.1:${HUB_LEAF[$i]} }
EOF
    "$NATS_BIN" -c "$f" -P "$RUN_DIR/${HUB_NAMES[$i]}.pid" >> "$RUN_DIR/logs/${HUB_NAMES[$i]}.log" 2>&1 &
    record_pid "$!" "${HUB_NAMES[$i]}"
  done
  for i in 0 1 2; do wait_http "http://127.0.0.1:${HUB_MON[$i]}/healthz" "${HUB_NAMES[$i]}"; done
  log "hub cluster up (${HUB_NAMES[*]})"

  # ── Edge leaf nodes (JS enabled, own domain, leaf to the matching hub node) ──
  for i in 0 1 2; do
    local f="$RUN_DIR/conf/${EDGE_NAMES[$i]}.conf"
    cat > "$f" <<EOF
server_name: ${EDGE_NAMES[$i]}
listen: 127.0.0.1:${EDGE_CLIENT[$i]}
http: 127.0.0.1:${EDGE_MON[$i]}
jetstream { domain: ${EDGE_DOMAIN[$i]}, store_dir: "$RUN_DIR/js/${EDGE_NAMES[$i]}" }
leafnodes { remotes: [ { url: "nats://127.0.0.1:${HUB_LEAF[$i]}" } ] }
EOF
    "$NATS_BIN" -c "$f" -P "$RUN_DIR/${EDGE_NAMES[$i]}.pid" >> "$RUN_DIR/logs/${EDGE_NAMES[$i]}.log" 2>&1 &
    record_pid "$!" "${EDGE_NAMES[$i]}"
  done
  for i in 0 1 2; do wait_http "http://127.0.0.1:${EDGE_MON[$i]}/healthz" "${EDGE_NAMES[$i]}"; done
  log "edge leaf nodes up (${EDGE_NAMES[*]})"

  # ── MachMQTT brokers (one per edge) ──
  for i in 0 1 2; do
    local f="$RUN_DIR/conf/${BROKER_NAMES[$i]}.yaml"
    cat > "$f" <<EOF
nats:
  url: "nats://127.0.0.1:${EDGE_CLIENT[$i]}"
mqtt:
  host: "127.0.0.1"
  port: ${BROKER_MQTT[$i]}
auth:
  type: "none"
admin:
  addr: "127.0.0.1:${BROKER_ADMIN[$i]}"
  bearer_token: "${BROKER_ADMIN_TOKEN}"
  allow_kick_endpoint: true
  allow_drain_endpoint: true
  allow_reload_endpoint: true
  clients_snapshot_interval: 2s
logging:
  level: "info"
observability:
  instance_name: "${BROKER_NAMES[$i]}"
  metrics:
    enabled: true
    interval: 2s
licensing:
  cluster_secret: "${CLUSTER_SECRET}"
EOF
    MACHMQTT_LICENSE_KEY="$MACHMQTT_LICENSE_KEY" "$MACHMQTT_BIN" -config "$f" \
      >> "$RUN_DIR/logs/${BROKER_NAMES[$i]}.log" 2>&1 &
    record_pid "$!" "${BROKER_NAMES[$i]}"
  done
  for i in 0 1 2; do wait_http "http://127.0.0.1:${BROKER_ADMIN[$i]}/readyz" "${BROKER_NAMES[$i]}"; done
  log "MachMQTT brokers up (${BROKER_NAMES[*]})"

  # ── Dashboard ──
  local secret; secret="$(head -c 32 /dev/urandom | xxd -p -c 64 2>/dev/null || echo demo-secret)"
  cat > "$RUN_DIR/conf/dashboard.yaml" <<EOF
listen: "127.0.0.1:${DASH_PORT}"
poll_interval: 2s
metrics_retention: 24h
session_secret: "${secret}"
data_dir: "$RUN_DIR/dash-data"
EOF
  "$RUN_DIR/dashboard" -config "$RUN_DIR/conf/dashboard.yaml" >> "$RUN_DIR/logs/dashboard.log" 2>&1 &
  record_pid "$!" "dashboard"
  wait_http "http://127.0.0.1:${DASH_PORT}/" "dashboard"

  configure_dashboard
  start_traffic

  echo
  log "READY — open the dashboard for screenshots:"
  echo "    URL:   http://127.0.0.1:${DASH_PORT}"
  echo "    Login: ${DASH_USER} / ${DASH_PASS}"
  echo "    Topology:  http://127.0.0.1:${DASH_PORT}/topology"
  echo "    Fleet:     http://127.0.0.1:${DASH_PORT}/mqtt"
  echo
  log "stop with: $0 down"
}

configure_dashboard() {
  local jar="$RUN_DIR/cookies.txt"
  # First-run admin is admin/admin with a forced change; log in, then set the
  # documented demo password so screenshots aren't interrupted by the prompt.
  curl -s -c "$jar" -X POST "http://127.0.0.1:${DASH_PORT}/api/login" \
    -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin"}' >/dev/null
  local uid
  uid="$(curl -s -b "$jar" "http://127.0.0.1:${DASH_PORT}/api/me" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')"
  if [ -n "$uid" ]; then
    # Capture the cookie the password change reissues (-c): changing the password
    # invalidates the pre-change session, and the response carries a fresh cookie
    # that later requests must use.
    curl -s -b "$jar" -c "$jar" -X PUT "http://127.0.0.1:${DASH_PORT}/api/users/${uid}/password" \
      -H 'Content-Type: application/json' \
      -d "{\"old_password\":\"admin\",\"new_password\":\"${DASH_PASS}\"}" >/dev/null || true
  fi

  # Build the environment: monitor all 6 servers, configure the 3 broker admin
  # URLs, take MQTT metrics over NATS push (subject $MQTT5, via the hub), and
  # disable connz-scan discovery (the brokers' admin ports aren't the default).
  local servers="" bridges=""
  for p in "${HUB_MON[@]}" "${EDGE_MON[@]}"; do servers="$servers{\"url\":\"http://127.0.0.1:$p\"},"; done
  servers="${servers%,}"
  for i in 0 1 2; do bridges="$bridges{\"name\":\"${BROKER_NAMES[$i]}\",\"url\":\"http://127.0.0.1:${BROKER_ADMIN[$i]}\"},"; done
  bridges="${bridges%,}"

  curl -s -b "$jar" -X POST "http://127.0.0.1:${DASH_PORT}/api/admin/clusters" \
    -H 'Content-Type: application/json' -d "{
      \"name\":\"edge-fleet\",
      \"servers\":[${servers}],
      \"mqtt_bridges\":[${bridges}],
      \"admin_token\":\"${BROKER_ADMIN_TOKEN}\",
      \"mqtt_discovery\":{\"enabled\":false},
      \"nats_conn\":{\"urls\":[\"nats://127.0.0.1:${HUB_CLIENT[0]}\"],\"subject_prefix\":\"\$MQTT5\"}
    }" >/dev/null
  log "dashboard configured (environment 'edge-fleet': 6 servers, 3 brokers)"
}

start_traffic() {
  # Per-edge spiky MQTT traffic (lights each bridge + edge node).
  for i in 0 1 2; do
    "$RUN_DIR/loadgen" -mode mqtt -url "mqtt://127.0.0.1:${BROKER_MQTT[$i]}" \
      -prefix "e$((i+1))" -subs 2 -pubs 3 -spiky \
      >> "$RUN_DIR/logs/loadgen-mqtt-$((i+1)).log" 2>&1 &
    record_pid "$!" "loadgen-mqtt-$((i+1))"
  done
  # Cross-edge core-NATS flow (lights the leaf + route links — flow back & forth).
  "$RUN_DIR/loadgen" -mode nats \
    -urls "nats://127.0.0.1:${EDGE_CLIENT[0]},nats://127.0.0.1:${EDGE_CLIENT[1]},nats://127.0.0.1:${EDGE_CLIENT[2]}" \
    -pubs 2 -spiky >> "$RUN_DIR/logs/loadgen-nats.log" 2>&1 &
  record_pid "$!" "loadgen-nats"
  log "traffic started (spiky MQTT per edge + cross-edge NATS flow)"
}

cmd_down() {
  [ -f "$PIDS_FILE" ] || { warn "nothing to stop (no $PIDS_FILE)"; return 0; }
  while read -r pid label; do
    [ -n "${pid:-}" ] || continue
    if kill "$pid" 2>/dev/null; then log "stopped $label ($pid)"; fi
  done < "$PIDS_FILE"
  rm -f "$PIDS_FILE"
  log "all stopped (logs kept in $RUN_DIR/logs)"
}

cmd_status() {
  [ -f "$PIDS_FILE" ] || { warn "not running"; return 0; }
  printf '%-20s %-8s %s\n' "PROCESS" "PID" "STATE"
  while read -r pid label; do
    [ -n "${pid:-}" ] || continue
    if kill -0 "$pid" 2>/dev/null; then printf '%-20s %-8s %s\n' "$label" "$pid" "alive"
    else printf '%-20s %-8s %s\n' "$label" "$pid" "DEAD"; fi
  done < "$PIDS_FILE"
  echo
  echo "dashboard: http://127.0.0.1:${DASH_PORT}  (login ${DASH_USER}/${DASH_PASS})"
}

cmd_logs() { tail -n 40 -F "$RUN_DIR/logs/dashboard.log" "$RUN_DIR/logs/${BROKER_NAMES[0]}.log"; }

cmd_shots() {
  [ -d "$REPO_ROOT/ui/node_modules/@playwright/test" ] || die "Playwright not installed — run 'npm install' in $REPO_ROOT/ui first"
  local out="$RUN_DIR/shots"
  log "capturing screenshots to $out …"
  ( cd "$REPO_ROOT/ui" && BASE_URL="http://127.0.0.1:${DASH_PORT}" OUT_DIR="$out" \
      DASH_USER="$DASH_USER" DASH_PASS="$DASH_PASS" node "$SCRIPT_DIR/screenshots.mjs" )
  log "screenshots in $out"
}

case "${1:-}" in
  up)     cmd_up ;;
  down)   cmd_down ;;
  status) cmd_status ;;
  logs)   cmd_logs ;;
  shots)  cmd_shots ;;
  *) echo "usage: $0 {up|down|status|logs|shots}"; exit 1 ;;
esac
