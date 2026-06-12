# VayuPress Makefile
# Usage: make <target>

.PHONY: help dev build test test-race lint vuln check-size clean

BINARY     := vayupress
SRC_DIR    := /var/www/vayupress/src
GZIP_LIMIT := 47185920  # 45 MB in bytes

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

dev: ## Run the server locally (requires env vars)
	@echo "Starting VayuPress dev server..."
	@[ -n "$$API_KEY" ] || (echo "ERROR: API_KEY not set"; exit 1)
	@mkdir -p $${CACHE_DIR:-/tmp/vayupress-cache}
	go run $(SRC_DIR)/main.go

build: ## Build the binary
	go build -ldflags="-s -w" -o $(BINARY) $(SRC_DIR)/main.go
	@echo "Binary size: $$(du -sh $(BINARY) | cut -f1)"

test: ## Run all tests
	go test ./... -v -count=1

test-race: ## Run tests with race detector (required before PR)
	go test -race ./... -count=1

lint: ## Run golangci-lint
	golangci-lint run

vuln: ## Run vulnerability scan
	govulncheck ./...

check-size: build ## Check binary size against 45 MB limit
	@SIZE=$$(gzip -c $(BINARY) | wc -c); \
	echo "Compressed binary: $$(echo "$$SIZE / 1048576" | bc) MB"; \
	if [ $$SIZE -gt $(GZIP_LIMIT) ]; then \
		echo "FAIL: Binary exceeds 45 MB limit ($$SIZE bytes)"; exit 1; \
	else \
		echo "OK: Binary within limit"; \
	fi

check-docs: ## Verify all required documentation files exist
	@MISSING=0; \
	for f in README.md CHANGELOG.md SECURITY.md GOVERNANCE.md ETHICS.md CONTRIBUTING.md CODE_OF_CONDUCT.md \
	          docs/INSTALLATION.md docs/API-REFERENCE.md docs/ARCHITECTURE.md docs/DEVELOPMENT.md; do \
		[ -f "$$f" ] || { echo "MISSING: $$f"; MISSING=1; }; \
	done; \
	[ $$MISSING -eq 0 ] && echo "OK: All required docs present" || exit 1

dry-run: ## Run deploy script in dry-run mode
	sudo ./scripts/deploy-vayupress.sh --dry-run

clean: ## Remove built binary
	rm -f $(BINARY)
