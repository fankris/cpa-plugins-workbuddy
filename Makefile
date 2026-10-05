.DEFAULT_GOAL := build
.PHONY: build frontend frontend-test test lint clean release tag

GO ?= go
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
RELEASE_TARGETS ?= linux/amd64 linux/arm64
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

# Cross-compile for all supported platforms (requires cross C toolchains).
# On a Linux host without osxcross, only linux/* targets will succeed.
release: clean
	@set -eu; mkdir -p dist; \
	for target in $(RELEASE_TARGETS); do \
	  os=$${target%/*}; arch=$${target#*/}; \
	  out=dist/workbuddy_$(VERSION)_$${os}_$${arch}; \
	  mkdir -p "$$out"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=1 $(GO) build -buildmode=c-shared -ldflags "$(LDFLAGS)" -o "$$out/workbuddy.so" .; \
	  test -s "$$out/workbuddy.so"; \
	  cp README.md README_CN.md LICENSE "$$out/"; \
	  if [ -d licenses ]; then cp -R licenses "$$out/"; fi; \
	  if [ -f THIRD_PARTY_NOTICES.md ]; then cp THIRD_PARTY_NOTICES.md "$$out/"; fi; \
	  (cd dist && zip -qr "workbuddy_$(VERSION)_$${os}_$${arch}.zip" "workbuddy_$(VERSION)_$${os}_$${arch}"); \
	  echo "built $$out"; \
	done

# Tag a new release (default: VERSION file; override with VERSION=...).
tag:
	git tag -a $(TAG_VERSION) -m "$(TAG_VERSION)"
	git push origin $(TAG_VERSION)
