# iftahonline — build, test and quality gates. `make help` lists targets.
# The repository path may contain spaces or non-ASCII characters, so every
# path here is relative and every script uses "$PWD".

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
MAKEFLAGS += --no-builtin-rules

BIN      := bin
DASH     := web/dashboard
COMPOSE  := docker compose -f deploy/compose/docker-compose.yml
GOLANGCI := $(BIN)/golangci-lint
SQLC     := $(BIN)/sqlc
GITLEAKS := $(BIN)/gitleaks
TRIVY    := $(BIN)/trivy

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
IMAGE   ?= iftahonline:$(VERSION)

FUZZTIME ?= 10s
COVER_MIN ?= 80
# Coverage is enforced for business-module domain/app packages (CLAUDE.md)
# and for the platform packages that hold core rules.
COVER_MATCH := -match '/internal/modules/[^/]+/internal/(domain|app)$$' \
	-match '/internal/platform/(money|phone|ulid|tenant|event|errs|problem|safehttp|config|retry|logging|authz|jobs|ratelimit|secretbox|audit)$$'

API_ADDR        ?= 127.0.0.1:18080
STOREFRONT_ADDR ?= 127.0.0.1:18081

GO_GENERATED := internal/server/apigen internal/server/dbgen internal/modules/*/internal/httpapi/*.gen.go internal/modules/*/internal/store/*db internal/server/views/*_templ.go $(DASH)/src/api/schema.d.ts

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## ---- setup -------------------------------------------------------------

.PHONY: tools
tools: ## Install pinned tools (bin/) and dependencies
	./scripts/tools.sh
	go mod download
	$(MAKE) --no-print-directory dash-deps

$(DASH)/node_modules/.installed: $(DASH)/package-lock.json
	cd $(DASH) && npm ci --no-audit --no-fund
	touch "$@"

.PHONY: dash-deps
dash-deps: $(DASH)/node_modules/.installed

## ---- code generation ---------------------------------------------------

.PHONY: gen
gen: dash-deps ## Regenerate code: oapi-codegen, sqlc, templ, TS client
	go tool oapi-codegen -config api/oapi-codegen.system.yaml api/openapi.yaml
	go tool oapi-codegen -config api/oapi-codegen.tenancy.yaml api/openapi.yaml
	go tool oapi-codegen -config api/oapi-codegen.identity.yaml api/openapi.yaml
	$(SQLC) generate
	go tool templ generate -path internal/server/views
	cd $(DASH) && npm run --silent gen:api

.PHONY: gen-check
gen-check: gen ## Fail if generated code is out of date
	@if [ -n "$$(git status --porcelain -- $(GO_GENERATED))" ]; then \
		git --no-pager diff --stat -- $(GO_GENERATED); git status --porcelain -- $(GO_GENERATED); \
		echo "gen-check: generated code is stale; run 'make gen' and commit"; exit 1; fi
	@echo "gen-check: generated code is up to date"

## ---- Go quality --------------------------------------------------------

.PHONY: fmt
fmt: ## Format Go code (generated files are left to their generators)
	{ gofmt -l cmd internal db; go tool goimports -l -local github.com/roshbaikfb/iftahonline cmd internal db; } | sort -u \
		| while read -r f; do grep -qm1 '^// Code generated .* DO NOT EDIT\.$$' "$$f" || echo "$$f"; done \
		| xargs -r go tool goimports -w -local github.com/roshbaikfb/iftahonline

.PHONY: fmt-check
fmt-check: ## Fail if Go code is not formatted (generated files excluded)
	@out="$$( { gofmt -l cmd internal db; go tool goimports -l -local github.com/roshbaikfb/iftahonline cmd internal db; } \
		| sort -u | while read -r f; do grep -qm1 '^// Code generated .* DO NOT EDIT\.$$' "$$f" || echo "$$f"; done)"; \
	if [ -n "$$out" ]; then echo "fmt-check: run 'make fmt':"; echo "$$out"; exit 1; fi; \
	echo "fmt-check: ok"

.PHONY: lint
lint: ## golangci-lint (staticcheck, gosec, errcheck, revive, dupl, depguard, ...)
	$(GOLANGCI) run ./...

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: test
test: ## Unit + integration tests (race, shuffled; needs Docker)
	go test -race -shuffle=on -count=1 -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: coverage
coverage: ## Enforce per-package coverage from coverage.out
	go run ./internal/tools/covercheck -profile coverage.out -min $(COVER_MIN) $(COVER_MATCH)

.PHONY: fuzz
fuzz: ## Run every fuzz target for FUZZTIME (default 10s)
	FUZZTIME=$(FUZZTIME) ./scripts/fuzz.sh

.PHONY: bench
bench: ## Benchmarks for storefront-path code
	go test -run='^$$' -bench=. -benchmem ./internal/platform/money/

.PHONY: vuln
vuln: ## govulncheck
	go tool govulncheck ./...

.PHONY: secrets
secrets: ## gitleaks over the full git history
	$(GITLEAKS) git --no-banner --redact --config .gitleaks.toml .

.PHONY: openapi-diff
openapi-diff: ## Fail on breaking OpenAPI changes vs the base branch
	./scripts/openapi-diff.sh

.PHONY: sqlc-vet
sqlc-vet: build ## sqlc vet against a migrated throwaway PostgreSQL
	SQLC=$(SQLC) PLATFORM_BIN=$(BIN)/platform ./scripts/sqlc-vet.sh

.PHONY: gates
gates: ## Prove the import, dupl and depguard gates fire
	GOLANGCI_LINT="$$PWD/$(GOLANGCI)" ./scripts/gatecheck.sh

.PHONY: systemd-verify
systemd-verify: ## Validate systemd units and their hardening score
	./scripts/systemd-verify.sh

## ---- dashboard ---------------------------------------------------------

.PHONY: dash-ci
dash-ci: dash-deps build ## Dashboard lint, typecheck, unit tests, Playwright smoke
	cd $(DASH) && npm run --silent lint && npm run --silent typecheck && npm run --silent test
	cd $(DASH) && PLATFORM_BIN="$$PWD/../../$(BIN)/platform" npx playwright test

## ---- build -------------------------------------------------------------

.PHONY: build
build: ## Static binary for this machine -> bin/platform
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/platform ./cmd/platform

.PHONY: build-linux
build-linux: ## Static linux/amd64 and linux/arm64 binaries -> dist/
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/platform-linux-$$arch ./cmd/platform; done
	cd dist && sha256sum platform-linux-* > SHA256SUMS

.PHONY: image
image: ## Container image (distroless, nonroot) for this platform
	docker buildx build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) -t $(IMAGE) --load .

.PHONY: image-scan
image-scan: ## Trivy scan of $(IMAGE): fail on HIGH/CRITICAL
	$(TRIVY) image --cache-dir .cache/trivy --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 $(IMAGE)

## ---- local development -------------------------------------------------

.PHONY: dev
dev: build ## Start dependencies, migrate, run api + storefront (Ctrl-C stops)
	$(COMPOSE) up -d --wait postgres redis meilisearch seaweedfs mailpit
	$(COMPOSE) up seaweedfs-init
	set -a; source .env.example; [ -f .env ] && source .env; set +a; \
	$(BIN)/platform migrate up; \
	HTTP_ADDR=$(API_ADDR) $(BIN)/platform api & api=$$!; \
	HTTP_ADDR=$(STOREFRONT_ADDR) $(BIN)/platform storefront & sf=$$!; \
	trap 'kill -TERM $$api $$sf 2>/dev/null; wait' INT TERM; \
	echo "api: http://$(API_ADDR)  storefront: http://$(STOREFRONT_ADDR)  mail: http://127.0.0.1:18025"; \
	wait

.PHONY: dev-down
dev-down: ## Stop local dependencies (keeps data volumes)
	$(COMPOSE) down

.PHONY: loadtest-smoke
loadtest-smoke: ## k6 smoke against BASE_URL (default: make dev's API)
	docker run --rm -i --add-host=host.docker.internal:host-gateway \
		-e BASE_URL=$${BASE_URL:-http://host.docker.internal:18080} grafana/k6:1.8.2 run - < loadtest/smoke.js

## ---- the pipeline ------------------------------------------------------

.PHONY: ci
ci: ## Full pipeline (CLAUDE.md): run before saying a task is done
	$(MAKE) --no-print-directory gen-check
	$(MAKE) --no-print-directory fmt-check
	$(MAKE) --no-print-directory lint
	$(MAKE) --no-print-directory vet
	$(MAKE) --no-print-directory test
	$(MAKE) --no-print-directory coverage
	$(MAKE) --no-print-directory fuzz
	$(MAKE) --no-print-directory vuln
	$(MAKE) --no-print-directory secrets
	$(MAKE) --no-print-directory openapi-diff
	$(MAKE) --no-print-directory sqlc-vet
	$(MAKE) --no-print-directory gates
	$(MAKE) --no-print-directory systemd-verify
	$(MAKE) --no-print-directory dash-ci
	@echo "ci: all gates passed"
