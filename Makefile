.DEFAULT_GOAL := build
.PHONY: build frontend frontend-test test lint clean release tag

GO ?= go
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
TAG_VERSION = $(if $(filter v%,$(VERSION)),$(VERSION),v$(VERSION))
LDFLAGS := -X main.version=$(VERSION)

# Rebuild the embedded browser assets using pinned npm dependencies.
frontend:
	cd frontend && npm ci --no-audit --no-fund && npm run typecheck && npm run build

frontend-test:
	node --test tests/frontend-contracts.mjs tests/native-config-contract.test.cjs

# Default target: build the plugin for the current platform.
build:
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -ldflags "$(LDFLAGS)" -o workbuddy.so .

# Run all tests with race detector.
test:
	$(GO) test -race -count=1 ./...

# Lint everything (requires gofmt + go vet; staticcheck/gocritic/unparam optional).
lint:
	@test -z "$$($(GO)fmt -l .)" || ($(GO)fmt -l . && exit 1)
	$(GO) vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; fi
	@if command -v gocritic >/dev/null 2>&1; then gocritic check ./...; fi
	@if command -v unparam >/dev/null 2>&1; then unparam ./...; fi

# Clean build artifacts.
clean:
	rm -f workbuddy.so workbuddy.h
	rm -rf bin/ dist/ coverage.out coverage.html

# One latest delivery bundle. First capture current UI with the local fixture:
# node tests/capture-delivery-ui.mjs (Playwright required).
# Missing/stale screenshot or validation evidence fails before replacing any ZIP.
release:
	mkdir -p artifacts
	CGO_ENABLED=1 $(GO) build -trimpath -buildmode=c-shared -ldflags "$(LDFLAGS)" -o artifacts/workbuddy.so .
	python3 tests/sdk-binary-probe.py
	python3 tests/render-delivery.py
	node tests/verify-delivery-html.mjs
	python3 tests/package-rebuild.py

# Tag a new release (default: VERSION file; override with VERSION=...).
tag:
	git tag -a $(TAG_VERSION) -m "$(TAG_VERSION)"
	git push origin $(TAG_VERSION)
