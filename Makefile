# VayuPress Makefile
# Usage: make <target>

.PHONY: help dev build test test-race test-integration lint vuln check-size \
        check-docs check-governance check-ethics check-security check-complexity \
        check-adrs test-migrations test-storage test-api-contracts bench \
        dry-run clean

BINARY     := vayupress
SRC_DIR    := /var/www/vayupress/src
GZIP_LIMIT := 47185920  # 45 MB in bytes
JS_GZ_LIMIT := 51200    # 50 KB in bytes

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2}'

dev: ## Run the server locally (requires env vars)
	@echo "Starting VayuPress dev server..."
	@[ -n "$$VAYU_API_KEY" ] || (echo "ERROR: VAYU_API_KEY not set"; exit 1)
	@mkdir -p $${VAYU_CACHE_DIR:-/tmp/vayupress-cache}
	go run $(SRC_DIR)/main.go

build: ## Build the binary
	CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o $(BINARY) $(SRC_DIR)/main.go
	@echo "Binary size: $$(du -sh $(BINARY) | cut -f1)"

test: ## Run unit tests
	go test ./... -v -count=1

test-race: ## Run tests with race detector (required before PR)
	go test -race ./... -count=1

test-integration: ## Run integration tests (requires sqlite3)
	go test -race -tags=integration ./... -count=1

test-migrations: ## Test forward migration + checksum verification (ADR-0034)
	go test -race -tags=integration -run TestMigration ./... -v

test-storage: ## Test WAL, backup, restore validation (ADR-0033, ADR-0042)
	go test -race -tags=integration -run TestWAL -run TestBackup ./... -v

test-api-contracts: ## Test all API endpoints against documented contracts
	go test -race -tags=integration -run TestAPI ./... -v

bench: ## Run performance benchmarks
	go test -bench=. -benchmem -benchtime=3s ./...

lint: ## Run golangci-lint (zero errors required)
	golangci-lint run

vuln: ## Run vulnerability scan (zero known CVEs required)
	govulncheck ./...

check-size: build ## Check binary size against 45 MB limit
	@SIZE=$$(gzip -c $(BINARY) | wc -c); \
	echo "Compressed binary: $$(( SIZE / 1048576 )) MB"; \
	if [ $$SIZE -gt $(GZIP_LIMIT) ]; then \
		echo "FAIL: Binary exceeds 45 MB limit ($$SIZE bytes)"; exit 1; \
	else \
		echo "OK: Binary within 45 MB limit"; \
	fi

check-docs: ## Verify all required documentation files exist
	@MISSING=0; \
	for f in README.md CHANGELOG.md SECURITY.md GOVERNANCE.md ETHICS.md CONTRIBUTING.md \
	          CODE_OF_CONDUCT.md GOVERNANCE-CONSTITUTION.md \
	          docs/INSTALLATION.md docs/API-REFERENCE.md docs/ARCHITECTURE.md \
	          docs/DEVELOPMENT.md docs/OPERATIONS.md docs/RELEASES.md \
	          docs/CI-GOVERNANCE.md docs/THREAT-MODEL.md docs/MAINTAINERS.md \
	          docs/SUSTAINABILITY.md docs/ETHICAL-REVIEW-PROCESS.md \
	          docs/rfc-template.md; do \
		[ -f "$$f" ] || { echo "MISSING: $$f"; MISSING=1; }; \
	done; \
	[ $$MISSING -eq 0 ] && echo "OK: All required docs present" || exit 1

check-adrs: ## Verify all required ADRs exist (ADR-0001, 0002, 0032-0043)
	@MISSING=0; \
	for adr in ADR-0001-sqlite-first ADR-0002-self-hosted-fonts \
	           ADR-0032-plugin-pool-waitgroup ADR-0033-wal-adaptive-checkpoint \
	           ADR-0034-migration-checksum-drift ADR-0035-dead-letter-queue-safety \
	           ADR-0036-csp-nonce ADR-0037-pprof-rate-limit \
	           ADR-0038-vacuum-cooldown ADR-0039-deploy-sourced-components \
	           ADR-0040-config-versioning ADR-0041-health-contracts \
	           ADR-0042-backup-restore-automation ADR-0043-integration-tests; do \
		ls docs/adr/$${adr}*.md >/dev/null 2>&1 || { echo "MISSING ADR: $$adr"; MISSING=1; }; \
	done; \
	[ $$MISSING -eq 0 ] && echo "OK: All 14 required ADRs present" || exit 1

check-governance: ## Verify all 12 Prompts present in Constitution
	@MISSING=0; \
	for p in "Prompt 1" "Prompt 2" "Prompt 3" "Prompt 4" "Prompt 5" "Prompt 6" \
	          "Prompt 7" "Prompt 8" "Prompt 9" "Prompt 10" "Prompt 11" "Prompt 12"; do \
		grep -q "$$p" GOVERNANCE-CONSTITUTION.md \
			&& echo "OK: $$p" \
			|| { echo "MISSING: $$p in GOVERNANCE-CONSTITUTION.md"; MISSING=1; }; \
	done; \
	[ $$MISSING -eq 0 ] || exit 1

check-ethics: ## Verify Ethical AI Charter sections in ETHICS.md
	@MISSING=0; \
	for item in "User Consent" "Transparency" "No Training" "Local-First" \
	            "Bias" "Accountability" "No Autonomous"; do \
		grep -qi "$$item" ETHICS.md \
			&& echo "OK: $$item" \
			|| { echo "MISSING: $$item in ETHICS.md"; MISSING=1; }; \
	done; \
	[ $$MISSING -eq 0 ] || exit 1

check-security: ## Verify SECURITY.md has required sections
	@MISSING=0; \
	for item in "security@vayupress.com" "72" "CVSS" "CVE" "advisory" \
	            "Strict-Transport-Security" "Content-Security-Policy"; do \
		grep -qi "$$item" SECURITY.md \
			&& echo "OK: $$item" \
			|| { echo "MISSING: $$item in SECURITY.md"; MISSING=1; }; \
	done; \
	[ $$MISSING -eq 0 ] || exit 1

check-complexity: ## Check cyclomatic complexity (advisory — gocyclo required)
	@command -v gocyclo >/dev/null 2>&1 \
		&& gocyclo -over 15 . \
		|| echo "ADVISORY: gocyclo not installed; run: go install github.com/fzipp/gocyclo/cmd/gocyclo@latest"

check-threat-model: ## Verify threat model has all required sections
	@MISSING=0; \
	for section in "Trust Boundaries" "Entry Points" "Assets" "Threat Actors" "Mitigations"; do \
		grep -q "$$section" docs/THREAT-MODEL.md \
			&& echo "OK: $$section" \
			|| { echo "MISSING: $$section in docs/THREAT-MODEL.md"; MISSING=1; }; \
	done; \
	[ $$MISSING -eq 0 ] || exit 1

dry-run: ## Run deploy script in dry-run mode
	sudo ./scripts/deploy-vayupress.sh --dry-run

clean: ## Remove built binary
	rm -f $(BINARY)
