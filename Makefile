.PHONY: build clean test test-unit test-integration lint fmt vet generate-docs

# Prefer arm64 Homebrew Go; unset GOROOT to avoid stale env
GO := $(shell which /opt/homebrew/bin/go 2>/dev/null || which go)
unexport GOROOT

# golangci-lint version
GOLANGCI_LINT_VERSION := v2.6.0

# Use custom FFmpeg if available
ifneq (,$(wildcard bin/ffmpeg/ffmpeg))
  export VEO_FFMPEG := $(CURDIR)/bin/ffmpeg/ffmpeg
  export VEO_FFPROBE := $(CURDIR)/bin/ffmpeg/ffprobe
endif

build:
	$(GO) build -o veo ./cmd/veo/

clean:
	rm -f veo

# Unit tests only (fast, no FFmpeg required, CI-safe)
test: test-unit

test-unit:
	$(GO) test -tags=unit -race -count=1 -parallel=4 ./...

# Integration tests (requires FFmpeg + test assets)
test-integration:
	$(GO) test -tags=integration -race -count=1 -timeout=10m ./...

# All tests
test-all:
	$(GO) test -tags="unit integration" -race -count=1 -timeout=10m ./...

lint:
	@which golangci-lint > /dev/null 2>&1 || \
		(echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..." && \
		$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION))
	golangci-lint run --timeout 3m

fmt:
	$(GO) fmt ./...
	@which goimports > /dev/null 2>&1 && goimports -local github.com/terranvigil/veo -w . || true

vet:
	$(GO) vet ./...

generate-docs:
	$(GO) run scripts/generate-doc-charts.go
