#!/usr/bin/env sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"

go test -run '^$' -bench . -benchmem -count 5 ./internal/store ./internal/collector ./internal/api
