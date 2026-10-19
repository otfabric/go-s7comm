# Self-documented Makefile (https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html)
# Run 'make' or 'make help' to list targets.

.DEFAULT_GOAL := help

.PHONY: help all check test test-race coverage cover bench lint lint-ci vuln fmt vet clean test-interop interop

# Parent otfabric/go.work may pin older modules; isolate toolchain commands.
export GOWORK := off

# Pinned snap7-interop v0.3.0 multi-arch index digests (override to test other builds).
SNAP_INTEROP_NATIVE_IMAGE ?= ghcr.io/otfabric/snap7-interop-native@sha256:66e3826813005bfacb8510ce5de33119315499325b35bf616dcf61de0506a342
SNAP_INTEROP_PYTHON_IMAGE ?= ghcr.io/otfabric/snap7-interop-python@sha256:a53c81d7b6acb042a010fc1c9932f1f92b8c337e2555e3200938f4b0b46e3d54

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: ## Format, vet, and test
	@echo "Running all: fmt, vet, test"
	@$(MAKE) fmt vet test

# Build tag for the interop package (excluded from default ./... builds).
INTEROP_TAGS := interop

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

lint: ## Run staticcheck (includes -tags=interop for ./interop)
	@echo "Running staticcheck"
	@staticcheck -tags=$(INTEROP_TAGS) ./...

lint-ci: ## Run golangci-lint (build-tags include interop; see .golangci.yml)
	@echo "Running golangci-lint"
	@golangci-lint run ./...

vuln: ## Run govulncheck
	@echo "Running govulncheck"
	@govulncheck ./...

fmt: ## Format Go code with gofmt
	@echo "Running gofmt"
	@gofmt -w .
	@echo "Running go fmt"
	@go fmt ./...

vet: ## Run go vet (includes -tags=interop compile of ./interop)
	@echo "Running go vet"
	@go vet -tags=$(INTEROP_TAGS) ./...
	@echo "Typechecking interop package (no tests run)"
	@go test -tags=$(INTEROP_TAGS) -count=1 -run=^$$ ./interop/

clean: ## Remove generated coverage artifacts
	@rm -f coverage.out coverage.html

test-interop: ## Run SNAP7 interop suite (Docker; full fixture matrix × native+python)
	@echo "Running interop tests against snap7-interop servers"
	@SNAP_INTEROP_NATIVE_IMAGE="$(SNAP_INTEROP_NATIVE_IMAGE)" \
		SNAP_INTEROP_PYTHON_IMAGE="$(SNAP_INTEROP_PYTHON_IMAGE)" \
		go test -tags=interop -count=1 -race ./interop/...

interop: test-interop ## Alias for test-interop
