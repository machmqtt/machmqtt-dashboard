#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
baseline_ref="${GREMLINS_DIFF:-HEAD}"
go_command="${GO:-go}"
work_dir="$(mktemp -d)"
report_dir="$repo_root/mutation-reports"

cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

# Gremlins clones its input once per worker. Constructing a Go-only module
# avoids copying .git, node_modules, browser artifacts, and mutation sandboxes.
git -C "$repo_root" archive "$baseline_ref" -- go.mod go.sum cmd internal | tar -x -C "$work_dir"
git -C "$work_dir" init -q
git -C "$work_dir" add .
git -C "$work_dir" -c user.name=mutation-test -c user.email=mutation@test.invalid commit -qm baseline

rsync -a "$repo_root/go.mod" "$repo_root/go.sum" "$work_dir/"
rsync -a --delete "$repo_root/cmd/" "$work_dir/cmd/"
rsync -a --delete "$repo_root/internal/" "$work_dir/internal/"
git -C "$work_dir" add -N .

mkdir -p "$report_dir"
(
  cd "$work_dir"
  "$go_command" run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash \
    --diff HEAD \
    --output "$report_dir/gremlins.json" \
    --output-statuses lc \
    --threshold-efficacy 90 \
    --threshold-mcover 95 \
    --workers 8 \
    --test-cpu 2
)
