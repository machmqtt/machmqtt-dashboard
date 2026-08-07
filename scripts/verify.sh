#!/usr/bin/env bash
# Mirrors .github/workflows/ci.yml so a push cannot turn CI red.
#
# Stage names match the CI step names to keep the two readable side by side.
# Pass --quick to skip the mutation and enterprise-auth suites; CI still runs
# them, so a quick run is only safe for work that is not about to be pushed.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

GOLANGCI_LINT="github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4"
GOVULNCHECK="golang.org/x/vuln/cmd/govulncheck@v1.6.0"
GO_PACKAGES=("./cmd/..." "./internal/...")
GO_VET_TARGETS=("./cmd/..." "./internal/..." "./test/enterprise-auth/app")

run_slow_suites=true
if [[ "${1:-}" == "--quick" ]]; then
  run_slow_suites=false
fi

current_stage="startup"
stage_started=0

stage() {
  current_stage="$1"
  stage_started=$(date +%s)
  printf '\n\033[1;34m==> %s\033[0m\n' "$1"
}

end_stage() {
  printf '\033[0;32m    ok (%ss)\033[0m\n' "$(($(date +%s) - stage_started))"
}

skip_stage() {
  printf '\n\033[1;34m==> %s\033[0m\n' "$1"
  printf '\033[0;33m    skipped (%s)\033[0m\n' "$2"
}

on_failure() {
  printf '\n\033[1;31mFAILED: %s\033[0m\n' "$current_stage" >&2
  printf 'Fix the failure above, then re-run scripts/verify.sh.\n' >&2
}
trap on_failure ERR

# The frontend build rewrites internal/api/dist/index.html with fresh asset
# hashes. Those assets are gitignored; the tracked file is only a placeholder so
# //go:embed dist/* compiles on a clone with no frontend build. Verifying must
# not dirty the working tree, so put it back the way we found it.
dist_dirty_at_start=$(git status --porcelain -- internal/api/dist)
restore_dist() {
  if [[ -z "$dist_dirty_at_start" ]]; then
    git checkout --quiet -- internal/api/dist 2>/dev/null || true
  fi
}
trap restore_dist EXIT

started=$(date +%s)

# --- quality-and-security -------------------------------------------------

stage "Go format check"
unformatted=$(gofmt -l cmd internal test/enterprise-auth/app)
if [[ -n "$unformatted" ]]; then
  echo "Files not formatted:"
  echo "$unformatted"
  exit 1
fi
end_stage

stage "Go vet"
go vet "${GO_VET_TARGETS[@]}"
end_stage

stage "golangci-lint"
go run "$GOLANGCI_LINT" run
end_stage

stage "Go coverage gate"
make test-go-coverage
end_stage

stage "Go race suite"
go test -race -count=1 -timeout 600s "${GO_PACKAGES[@]}"
end_stage

stage "Go vulnerability check"
go run "$GOVULNCHECK" "${GO_VET_TARGETS[@]}" | tee govulncheck.txt
end_stage

stage "Install frontend dependencies"
(cd ui && npm ci)
end_stage

stage "NPM audit"
(cd ui && npm audit --audit-level=moderate)
end_stage

stage "ESLint"
(cd ui && npm run lint)
end_stage

stage "Frontend coverage gate"
(cd ui && npm run test:coverage)
end_stage

stage "Build frontend"
(cd ui && npm run build)
end_stage

stage "Initial bundle budget"
(cd ui && npm run check:bundle)
end_stage

# Deliberately no dist restore here. Later stages serve the built SPA, and the
# committed index.html names asset hashes that no longer exist on disk, so
# restoring it mid-run leaves Playwright loading a page with no JavaScript. The
# rebuilt file is harmless to the mutation gates: both the applicability check
# below and scripts/test-mutation-go.sh scope their diffs to *.go. The exit trap
# puts it back once everything has finished with it.

# --- enterprise-auth ------------------------------------------------------

if $run_slow_suites; then
  stage "Playwright browser"
  if [[ "$(uname -s)" == "Linux" ]]; then
    (cd ui && npx playwright install --with-deps chromium)
  else
    # --with-deps installs Linux system packages and needs root; the browser
    # download alone is what the suite actually requires elsewhere.
    (cd ui && npx playwright install chromium)
  fi
  end_stage

  stage "OpenLDAP and Dex integration matrix"
  if ! docker info >/dev/null 2>&1; then
    echo "Docker is not running; this suite starts OpenLDAP and Dex containers." >&2
    exit 1
  fi
  make test-enterprise-auth
  end_stage
fi

# --- mutation (CI runs this on pull requests) -----------------------------

if $run_slow_suites; then
  # scripts/test-mutation-go.sh refuses a baseline that mutates nothing. That is
  # correct for an explicit mutation run, but wrong as a push gate: a change that
  # touches no Go source has nothing to mutate, and that is not a quality
  # failure. Decide applicability here, then let the tool enforce the score.
  mutation_baseline="${GREMLINS_DIFF:-$(git merge-base HEAD origin/dev 2>/dev/null || true)}"

  if [[ -z "$mutation_baseline" ]]; then
    stage "Mutation quality gates"
    echo "Cannot resolve a mutation baseline. Fetch origin/dev or set GREMLINS_DIFF." >&2
    exit 1
  fi

  if git diff --quiet "$mutation_baseline" -- '*.go' ':(exclude)*_test.go'; then
    skip_stage "Go mutation gate" "no Go source changes since ${mutation_baseline:0:12}"
  else
    stage "Go mutation gate"
    GREMLINS_DIFF="$mutation_baseline" make test-mutation-go
    end_stage
  fi

  # Stryker mutates a fixed list of files under ui/src, so it is only meaningful
  # when one of them could have changed.
  if git diff --quiet "$mutation_baseline" -- 'ui/src'; then
    skip_stage "Frontend mutation gate" "no ui/src changes since ${mutation_baseline:0:12}"
  else
    stage "Frontend mutation gate"
    make test-mutation-ui
    end_stage
  fi
fi

# --- release-verification -------------------------------------------------

stage "Cross-compile release targets"
make build-all
end_stage

stage "Build and smoke-test release candidate"
mkdir -p bin
go build -trimpath -ldflags="-s -w -X main.version=$(git rev-parse HEAD)" -o bin/machmqtt-dashboard ./cmd/machmqtt-dashboard
bin/machmqtt-dashboard -version
end_stage

if $run_slow_suites; then
  stage "Representative benchmarks"
  scripts/benchmark-release.sh | tee benchmark.txt
  end_stage
fi

trap - ERR
elapsed=$(($(date +%s) - started))
printf '\n\033[1;32mAll CI gates passed locally in %sm%ss.\033[0m\n' "$((elapsed / 60))" "$((elapsed % 60))"
if ! $run_slow_suites; then
  printf '\033[1;33mNote: --quick skipped mutation, enterprise-auth and benchmarks. CI still runs them.\033[0m\n'
fi
