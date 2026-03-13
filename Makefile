BINARY    := vmsmith
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR := ./bin
LDFLAGS   := -ldflags "-s -w -X main.version=$(VERSION)"
WEB_DIR   := ./web

.PHONY: build install clean test lint fmt deps web web-install test-web-deps \
        docker-build docker-run docker-stop docker-logs docker-test docker-shell

# --- Full build (frontend + backend) ---
build: go.sum web
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/vmsmith

# Backend only (skip frontend rebuild)
build-go: go.sum
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/vmsmith

# Auto-generate go.sum when missing or when go.mod changes
go.sum: go.mod
	go mod tidy
	go mod download

install: build
	sudo cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed $(BINARY) to /usr/local/bin/"

clean:
	rm -rf $(BUILD_DIR)
	rm -rf internal/web/dist
	rm -rf $(WEB_DIR)/node_modules

# --- Frontend ---
web-install:
	cd $(WEB_DIR) && npm install

web: web-install
	cd $(WEB_DIR) && npm run build

# Dev mode: run Vite dev server with API proxy
web-dev:
	cd $(WEB_DIR) && npm run dev

# --- Test / Lint ---
test:
	go test -v -race ./...

test-unit:
	go test -v -race ./internal/store/... ./internal/config/... ./internal/vm/... ./internal/cli/... ./internal/storage/...

test-integration:
	go test -v -race ./internal/api/...

test-web:
	node tests/web/run-gui-tests.js

test-all: test test-web

lint:
	golangci-lint run ./...

fmt:
	gofmt -w -s .

deps:
	go mod tidy
	go mod download

# Install Playwright and Chromium for E2E tests (run once before test-web)
test-web-deps:
	npm install
	npx playwright install chromium

# --- Host dependencies ---
deps-ubuntu:
	bash scripts/install-deps-ubuntu.sh

deps-rocky:
	bash scripts/install-deps-rocky.sh

# --- Distribution ---
dist: web
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/vmsmith

# --- Development workflow ---
# Terminal 1: make dev-api   (Go daemon on :8080)
# Terminal 2: make dev-web   (Vite on :3000 with proxy to :8080)
dev-api: build-go
	$(BUILD_DIR)/$(BINARY) daemon start

dev-web: web-install
	cd $(WEB_DIR) && npm run dev

# --- Docker ---
DOCKER_IMAGE := vmsmith
DOCKER_TAG   := $(VERSION)

# Build the production runtime image
docker-build:
	docker build --target runtime -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest .

# Start an interactive shell in the container (libvirtd started by entrypoint).
# Uses docker compose when available, falls back to plain docker run.
docker-run:
	@if docker compose version >/dev/null 2>&1; then \
		docker compose run --rm vmsmith; \
	else \
		docker run -it --rm \
			--name vmsmith \
			--device /dev/kvm \
			--cap-add NET_ADMIN --cap-add NET_RAW --cap-add SYS_ADMIN \
			--security-opt apparmor:unconfined \
			--network host \
			-v vmsmith-data:/var/lib/vmsmith \
			-v vmsmith-db:/root/.vmsmith \
			$(DOCKER_IMAGE):latest; \
	fi

# Stop and remove the container
docker-stop:
	@if docker compose version >/dev/null 2>&1; then \
		docker compose down; \
	else \
		docker stop vmsmith 2>/dev/null || true; \
	fi

# Tail daemon logs
docker-logs:
	docker logs -f vmsmith

# Run the full Go test suite inside Docker (no real libvirtd required)
docker-test:
	docker build --target test -t $(DOCKER_IMAGE):test .
	docker run --rm $(DOCKER_IMAGE):test

# Open a shell in the running container
docker-shell:
	docker exec -it vmsmith bash
