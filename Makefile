GO ?= go
NPM ?= npm
NPX ?= npx

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build build-ui build-all dev-backend dev-frontend test docker-build clean

build-ui:
	cd ui && $(NPM) install && $(NPX) vite build

build: build-ui
	$(GO) build $(LDFLAGS) -o bin/nats-dashboard ./cmd/nats-dashboard

build-all: build-ui
	GOOS=linux   GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/nats-dashboard-linux-amd64       ./cmd/nats-dashboard
	GOOS=darwin  GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/nats-dashboard-darwin-amd64      ./cmd/nats-dashboard
	GOOS=darwin  GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/nats-dashboard-darwin-arm64      ./cmd/nats-dashboard

dev-backend:
	$(GO) run ./cmd/nats-dashboard -config config.yaml

dev-frontend:
	cd ui && $(NPX) vite

test:
	$(GO) test -count=1 -timeout 120s github.com/noodlebit/nats-dashboard/...

docker-build:
	docker build -t nats-dashboard .

clean:
	rm -rf bin/ internal/api/dist/assets/ ui/node_modules/
