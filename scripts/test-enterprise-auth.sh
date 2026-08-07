#!/usr/bin/env sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose_file="$project_root/test/enterprise-auth/compose.yaml"
compose_project="machmqtt-enterprise-auth-test"
app_log=$(mktemp)
app_dir=$(mktemp -d)
app_pid=""

cleanup() {
  status=$?
  if [ -n "$app_pid" ]; then
    kill "$app_pid" 2>/dev/null || true
    wait "$app_pid" 2>/dev/null || true
  fi
  if [ "$status" -ne 0 ]; then
    cat "$app_log"
    docker compose --project-name "$compose_project" --file "$compose_file" logs --no-color
  fi
  rm -f "$app_log"
  rm -rf "$app_dir"
  docker compose --project-name "$compose_project" --file "$compose_file" down --volumes --remove-orphans
  exit "$status"
}
trap cleanup EXIT INT TERM

docker compose --project-name "$compose_project" --file "$compose_file" up --detach --pull missing

REAL_LDAP_URL=ldap://127.0.0.1:1389 \
REAL_OIDC_ISSUER_URL=http://127.0.0.1:5556/dex \
  go test -v -count=1 -timeout 120s -tags=enterprise_integration -run '^TestReal' ./internal/auth

# Build first and run the binary directly. `go run` execs the compiled program as
# a child, so killing the `go run` wrapper leaves the server holding port 18443.
# A leaked server then satisfies the readiness probe below on the next run, and
# the browser suite silently tests a stale binary instead of this checkout.
go build -o "$app_dir/app" ./test/enterprise-auth/app
"$app_dir/app" >"$app_log" 2>&1 &
app_pid=$!
ready=false
for _ in $(seq 1 120); do
  # Fail loudly rather than probing a port some other process owns.
  if ! kill -0 "$app_pid" 2>/dev/null; then
    echo "enterprise browser fixture exited before becoming ready" >&2
    exit 1
  fi
  if curl --insecure --fail --silent https://127.0.0.1:18443/livez >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.25
done
if [ "$ready" != true ]; then
  echo "enterprise browser fixture did not become ready" >&2
  exit 1
fi
(cd "$project_root/ui" && npm run test:e2e:enterprise)
