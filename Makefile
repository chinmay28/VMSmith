BINARY    := vmsmith
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR := ./bin
LDFLAGS   := -ldflags "-s -w -X main.version=$(VERSION)"
WEB_DIR   := ./web

.PHONY: build install clean test lint fmt deps web web-install test-web-deps \
       test-e2e test-e2e-cli test-e2e-api test-e2e-gui test-e2e-deps

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

# --- Real E2E tests (require running daemon + Rocky image) ---
# Install Python deps for E2E tests
test-e2e-deps:
	pip install -r tests/e2e/requirements.txt
	npx playwright install chromium

# All E2E tests (CLI + API + GUI)
test-e2e: test-e2e-cli test-e2e-api test-e2e-gui

# CLI E2E tests only
test-e2e-cli:
	cd tests/e2e && python -m pytest test_cli_vm_lifecycle.py test_cli_networking.py -v

# API E2E tests only
test-e2e-api:
	cd tests/e2e && python -m pytest test_api_vm_lifecycle.py test_api_networking.py -v

# GUI E2E tests (Playwright against live daemon)
test-e2e-gui:
	npx playwright test --config tests/e2e/playwright.config.js

# Run only networking E2E tests
test-e2e-networking:
	cd tests/e2e && python -m pytest -m networking -v

# Run only port forwarding E2E tests
test-e2e-portforward:
	cd tests/e2e && python -m pytest -m portforward -v

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
