# The factory's gate runs `make check` (see docs/factoryd/backlog/README.md).
GO ?= go
# Single source of truth for the linter version: local runs and CI both use it.
GOLANGCI_LINT_VERSION ?= v2.15.0

.PHONY: check fmt vet test lint build

check: fmt vet test lint build

fmt:
	@out=$$(gofmt -l . ); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	CGO_ENABLED=0 $(GO) vet ./...

test:
	CGO_ENABLED=0 $(GO) test -race=false -count=1 ./...

lint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

build:
	CGO_ENABLED=0 $(GO) build -o bin/ ./cmd/factoryd ./cmd/factory
