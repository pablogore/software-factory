# The factory's gate runs `make check` (see docs/factoryd/backlog/README.md).
GO ?= go

.PHONY: check fmt vet test lint build

check: fmt vet test lint build

fmt:
	@out=$$(gofmt -l . ); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	CGO_ENABLED=0 $(GO) vet ./...

test:
	CGO_ENABLED=0 $(GO) test -race=false -count=1 ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not installed: skipped"; fi

build:
	CGO_ENABLED=0 $(GO) build -o bin/ ./cmd/factoryd ./cmd/factory
