GO ?= go
NPM ?= npm
NPX ?= npx
GO_COVERAGE_MIN ?= 95
GO_COVERAGE_FILE ?= $(CURDIR)/coverage.out
# Left empty on purpose: a HEAD baseline makes a clean checkout produce an empty
# diff, so gremlins would mutate nothing and report success. Empty lets
# scripts/test-mutation-go.sh resolve the merge base with the integration branch.
GREMLINS_DIFF ?=

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build build-ui build-all dev-backend dev-frontend test test-go-coverage test-ui test-mutation test-mutation-go test-mutation-ui test-enterprise-auth benchmark-release docker-build clean verify verify-quick hooks

verify:
	./scripts/verify.sh

verify-quick:
	./scripts/verify.sh --quick

hooks:
	git config core.hooksPath .githooks
	@echo "pre-push hook enabled; it runs scripts/verify.sh before every push"

build-ui:
	cd ui && $(NPM) ci && $(NPM) run build

build: build-ui
	$(GO) build $(LDFLAGS) -o bin/machmqtt-dashboard ./cmd/machmqtt-dashboard

build-all: build-ui
	GOOS=linux   GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/machmqtt-dashboard-linux-amd64       ./cmd/machmqtt-dashboard
	GOOS=darwin  GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/machmqtt-dashboard-darwin-amd64      ./cmd/machmqtt-dashboard
	GOOS=darwin  GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/machmqtt-dashboard-darwin-arm64      ./cmd/machmqtt-dashboard
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/machmqtt-dashboard-windows-amd64.exe ./cmd/machmqtt-dashboard

dev-backend:
	$(GO) run ./cmd/machmqtt-dashboard -config config.yaml

dev-frontend:
	cd ui && $(NPX) vite

test: test-go-coverage test-ui

test-go-coverage:
	$(GO) test -count=1 -timeout 180s -coverprofile=$(GO_COVERAGE_FILE) ./cmd/... ./internal/...
	@coverage="$$($(GO) tool cover -func=$(GO_COVERAGE_FILE) | awk '/^total:/ {gsub("%", "", $$3); print $$3}')"; \
		echo "First-party Go coverage: $$coverage% (minimum $(GO_COVERAGE_MIN)%)"; \
		awk -v coverage="$$coverage" -v minimum="$(GO_COVERAGE_MIN)" 'BEGIN { exit !(coverage + 0 >= minimum + 0) }'
	@awk -v minimum="$(GO_COVERAGE_MIN)" 'NR > 1 { \
		split($$1, location, ":"); file = location[1]; marker = "/internal/"; start = index(file, marker); \
		if (start == 0) next; rest = substr(file, start + length(marker)); split(rest, path, "/"); package = path[1]; \
		total[package] += $$2; if ($$3 > 0) covered[package] += $$2 \
	} END { failed = 0; for (package in total) { coverage = 100 * covered[package] / total[package]; \
		printf "internal/%s coverage: %.1f%% (minimum %.1f%%)\n", package, coverage, minimum; if (coverage + 0.00001 < minimum) failed = 1 \
	} exit failed }' $(GO_COVERAGE_FILE)

test-ui:
	cd ui && $(NPM) ci && $(NPM) run lint && $(NPM) run test:coverage && $(NPM) run build && $(NPM) run check:bundle

test-mutation: test-mutation-go test-mutation-ui

test-mutation-go:
	GREMLINS_DIFF="$(GREMLINS_DIFF)" GO="$(GO)" ./scripts/test-mutation-go.sh

test-mutation-ui:
	cd ui && $(NPM) ci && $(NPM) run test:mutation

test-enterprise-auth:
	./scripts/test-enterprise-auth.sh

benchmark-release:
	./scripts/benchmark-release.sh

docker-build:
	docker build -t machmqtt-dashboard .

clean:
	rm -rf bin/ internal/api/dist/assets/ ui/node_modules/
