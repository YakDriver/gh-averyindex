GO ?= go

default: help

build: ## Build the extension binary
	@echo "make: Building..."
	@$(GO) build -o gh-averyindex .

ci: build fmt-check fix-check vet test ## [CI] Run every check CI runs

fix: ## Apply go fix
	@echo "make: Applying go fix..."
	@$(GO) fix ./...

fix-check: ## [CI] Go Fix Check / go fix
	@echo "make: Checking go fix..."
	@$(GO) fix -diff ./... || { echo "make: To fix, run: make fix"; exit 1; }

fmt: ## Apply gofmt
	@echo "make: Applying gofmt..."
	@gofmt -w .

fmt-check: ## [CI] Format Check / gofmt
	@echo "make: Checking gofmt..."
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "make: Files need gofmt:"; echo "$$files"; \
		echo "make: To fix, run: make fmt"; exit 1; \
	fi

help: ## Display this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-12s\033[0m %s\n", $$1, $$2}'

install: build ## Install the local build as a gh extension
	@gh extension remove averyindex >/dev/null 2>&1 || true
	@gh extension install .

test: ## Run unit tests
	@echo "make: Running tests..."
	@$(GO) test -count=1 ./...

vet: ## [CI] Vet / go vet
	@echo "make: Running go vet..."
	@$(GO) vet ./...

.PHONY: build ci default fix fix-check fmt fmt-check help install test vet
