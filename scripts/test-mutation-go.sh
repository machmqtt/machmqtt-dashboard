#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Gremlins only mutates lines that differ from the baseline. Defaulting to HEAD
# makes a clean checkout produce an empty diff, so the run would mutate nothing
# and report success without testing anything. Default instead to the merge base
# with the integration branch, which is the set of lines this branch introduces.
base_branch="${GREMLINS_BASE_BRANCH:-origin/dev}"
if [[ -n "${GREMLINS_DIFF:-}" ]]; then
  baseline_ref="$GREMLINS_DIFF"
elif baseline_ref="$(git -C "$repo_root" merge-base HEAD "$base_branch" 2>/dev/null)" && [[ -n "$baseline_ref" ]]; then
  :
else
  echo "error: cannot resolve a mutation baseline. Set GREMLINS_DIFF to a ref, or" >&2
  echo "       fetch $base_branch so a merge base can be computed." >&2
  exit 1
fi
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

# An empty diff means gremlins would mutate nothing and exit 0. That is a
# vacuous pass, not a passing mutation score — refuse it.
if git -C "$work_dir" diff --quiet HEAD; then
  echo "error: no Go changes against baseline $baseline_ref; nothing to mutate." >&2
  echo "       Refusing to report success for a mutation run that tested nothing." >&2
  exit 1
fi

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
