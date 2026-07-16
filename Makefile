GO ?= go
NPM ?= npm
NPX ?= npx

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build build-ui build-all dev-backend dev-frontend test docker-build clean

build-ui:
	cd ui && $(NPM) install && $(NPX) vite build

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

test:
	$(GO) test -count=1 -timeout 120s github.com/noodlebit/machmqtt-dashboard/...

docker-build:
	docker build -t machmqtt-dashboard .

clean:
	rm -rf bin/ internal/api/dist/assets/ ui/node_modules/
