# Self-documented Makefile (https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html)
# Run 'make' or 'make help' to list targets.

.DEFAULT_GOAL := help

.PHONY: help all check test test-race coverage cover bench lint lint-ci fmt vet clean test-interop interop

# Pinned snap7-interop v0.1.0 multi-arch index digests (override to test other builds).
SNAP_INTEROP_NATIVE_IMAGE ?= ghcr.io/otfabric/snap-interop-native@sha256:d630d021d58f52445c0e19d5ec19d58cf19f4316ca05ba2d8afeca8cb419a1db
SNAP_INTEROP_PYTHON_IMAGE ?= ghcr.io/otfabric/snap-interop-python@sha256:11baf7133f9d5ae6c282c159aa7dd4fdf2ed7be55784719514ad43a19a73b85d

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: ## Format, vet, and test
	@echo "Running all: fmt, vet, test"
	@$(MAKE) fmt vet test

check: fmt lint lint-ci vuln vet test coverage ## Run all checks

test: ## Run unit tests with race detector
	@echo "Running tests"
	@go test -count=1 -race ./...

test-race: test ## Alias for race-enabled test run

bench: ## Run benchmarks (discovery, probe, compare, wire parsers)
	@echo "Running benchmarks"
	@go test -count=1 -bench=. -benchmem ./...

coverage: ## Run tests with coverage (writes coverage.out)
	@echo "Running coverage"
	@go test -count=1 -race -coverprofile=coverage.out -covermode=atomic ./...

cover: coverage ## Open coverage report in browser
	@echo "Opening coverage report"
	@go tool cover -html=coverage.out

lint: ## Run staticcheck
	@echo "Running staticcheck"
	@staticcheck ./...

lint-ci: ## Run golangci-lint
	@echo "Running golangci-lint"
	@golangci-lint run ./...

vuln: ## Run govulncheck
	@echo "Running govulncheck"
	@govulncheck ./...

fmt: ## Format Go code with gofmt
	@echo "Running gofmt"
	@gofmt -w .

vet: ## Run go vet
	@echo "Running go vet"
	@go vet ./...

clean: ## Remove generated coverage artifacts
	@rm -f coverage.out coverage.html

test-interop: ## Run SNAP7 interop suite (Docker; full fixture matrix × native+python)
	@echo "Running interop tests against snap7-interop servers"
	@SNAP_INTEROP_NATIVE_IMAGE="$(SNAP_INTEROP_NATIVE_IMAGE)" \
		SNAP_INTEROP_PYTHON_IMAGE="$(SNAP_INTEROP_PYTHON_IMAGE)" \
		go test -tags=interop -count=1 -race ./interop/...

interop: test-interop ## Alias for test-interop
