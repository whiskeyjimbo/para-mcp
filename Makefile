SHELL    := /bin/bash
BINARY   := paras
MODULE   := github.com/whiskeyjimbo/paras
CMD      := ./cmd/paras
WIRECHECK := ./cmd/wirecheck

# Colors
BOLD  := \033[1m
RESET := \033[0m
GREEN := \033[32m
CYAN  := \033[36m
YELLOW := \033[33m
RED   := \033[31m
DIM   := \033[2m

# ── Default ────────────────────────────────────────────────────────────────────

.DEFAULT_GOAL := help

.PHONY: help
help:
	@printf '$(BOLD)paras$(RESET) — federation-aware MCP server for PARA vaults\n\n'
	@printf '$(BOLD)$(CYAN)Build$(RESET)\n'
	@printf '  $(GREEN)build$(RESET)            compile binary to ./$(BINARY)\n'
	@printf '  $(GREEN)install$(RESET)          install binary to $$GOPATH/bin\n'
	@printf '\n'
	@printf '$(BOLD)$(CYAN)Test$(RESET)\n'
	@printf '  $(GREEN)test$(RESET)             run all tests with race detector\n'
	@printf '  $(GREEN)test-short$(RESET)       run tests, skip integration (no Docker required)\n'
	@printf '  $(GREEN)test-cover$(RESET)       run tests with HTML coverage report\n'
	@printf '\n'
	@printf '$(BOLD)$(CYAN)Quality$(RESET)\n'
	@printf '  $(GREEN)fmt$(RESET)              format all Go source files\n'
	@printf '  $(GREEN)vet$(RESET)              run go vet\n'
	@printf '  $(GREEN)wirecheck$(RESET)        run AllowedScopes linter\n'
	@printf '  $(GREEN)lint$(RESET)             fmt + vet + wirecheck\n'
	@printf '\n'
	@printf '$(BOLD)$(CYAN)Docs$(RESET)\n'
	@printf '  $(GREEN)features-index$(RESET)   regenerate docs/features/FEATURES.md from frontmatter\n'
	@printf '\n'
	@printf '$(BOLD)$(CYAN)Maintenance$(RESET)\n'
	@printf '  $(GREEN)tidy$(RESET)             go mod tidy\n'
	@printf '  $(GREEN)clean$(RESET)            remove build artifacts\n'

# ── Build ──────────────────────────────────────────────────────────────────────

.PHONY: build
build:
	@printf '$(CYAN)→ building $(BINARY)...$(RESET)\n'
	@go build -o $(BINARY) $(CMD)
	@printf '$(GREEN)✓ $(BINARY) ready$(RESET)\n'

.PHONY: install
install:
	@printf '$(CYAN)→ installing $(BINARY)...$(RESET)\n'
	@go install $(CMD)
	@printf '$(GREEN)✓ installed to $(shell go env GOPATH)/bin/$(BINARY)$(RESET)\n'

# ── Test ───────────────────────────────────────────────────────────────────────

.PHONY: test
test:
	@printf '$(CYAN)→ running tests (race detector on)...$(RESET)\n'
	@go test -race ./...
	@printf '$(GREEN)✓ all tests passed$(RESET)\n'

.PHONY: test-short
test-short:
	@printf '$(CYAN)→ running tests (short, no Docker)...$(RESET)\n'
	@go test -short -race ./...
	@printf '$(GREEN)✓ all tests passed$(RESET)\n'

.PHONY: test-cover
test-cover:
	@printf '$(CYAN)→ running tests with coverage...$(RESET)\n'
	@go test -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@printf '$(GREEN)✓ coverage report: coverage.html$(RESET)\n'

# ── Quality ────────────────────────────────────────────────────────────────────

.PHONY: fmt
fmt:
	@printf '$(CYAN)→ formatting...$(RESET)\n'
	@go fmt ./...
	@printf '$(GREEN)✓ done$(RESET)\n'

.PHONY: vet
vet:
	@printf '$(CYAN)→ vetting...$(RESET)\n'
	@go vet ./...
	@printf '$(GREEN)✓ clean$(RESET)\n'

.PHONY: wirecheck
wirecheck:
	@printf '$(CYAN)→ running wirecheck linter...$(RESET)\n'
	@go build -o /tmp/wirecheck-bin $(WIRECHECK)
	@go vet -vettool=/tmp/wirecheck-bin ./... && printf '$(GREEN)✓ wirecheck clean$(RESET)\n'

.PHONY: lint
lint: fmt vet wirecheck
	@printf '$(GREEN)✓ all quality checks passed$(RESET)\n'

# ── Docs ───────────────────────────────────────────────────────────────────────

FEATURES_INDEX := docs/features/FEATURES.md
ACTIVE_DIR     := docs/features/active
ARCHIVE_DIR    := docs/features/archive

.PHONY: features-index
features-index:
	@printf '$(CYAN)→ regenerating features index...$(RESET)\n'
	@{ \
		active_count=$$(ls $(ACTIVE_DIR)/*.md 2>/dev/null | wc -l | tr -d ' '); \
		archive_count=$$(ls $(ARCHIVE_DIR)/*.md 2>/dev/null | wc -l | tr -d ' '); \
		echo '---'; \
		printf 'summary: "Feature catalog: %s active features, %s archived."\n' "$$active_count" "$$archive_count"; \
		echo '---'; \
		echo ''; \
		echo '# FEATURES'; \
		echo ''; \
		echo '> **Scope:** Owns feature catalog, status snapshot, and links to per-feature docs. Not system architecture, scheduling, or item-level rationale.'; \
		echo ''; \
		echo '## Status Legend'; \
		echo '- `planned`'; \
		echo '- `in-flight`'; \
		echo '- `shipped`'; \
		echo ''; \
		echo '## Active Features'; \
		for f in $(ACTIVE_DIR)/*.md; do \
			[ -f "$$f" ] || continue; \
			base=$$(basename "$$f" .md); \
			status=$$(grep '^status:' "$$f" | head -1 | sed 's/status: *//;s/"//g'); \
			subsystem=$$(grep '^subsystem:' "$$f" | head -1 | sed 's/subsystem: *//;s/"//g'); \
			phase=$$(grep '^phase:' "$$f" | head -1 | sed 's/phase: *//;s/"//g'); \
			printf '[%s](./%s/%s.md) - status: `%s` - subsystem: `%s` - phase: %s\n' \
				"$$base" "active" "$$base" "$$status" "$$subsystem" "$$phase" \
				| sed 's/^/- /'; \
		done; \
		echo ''; \
		echo '## Archived Features'; \
		for f in $(ARCHIVE_DIR)/*.md; do \
			[ -f "$$f" ] || continue; \
			base=$$(basename "$$f" .md); \
			status=$$(grep '^status:' "$$f" | head -1 | sed 's/status: *//;s/"//g'); \
			last_updated=$$(grep '^last_updated:' "$$f" | head -1 | sed 's/last_updated: *//;s/"//g'); \
			printf '[%s](./%s/%s.md) - status: `%s` - archived: %s\n' \
				"$$base" "archive" "$$base" "$$status" "$$last_updated" \
				| sed 's/^/- /'; \
		done; \
		echo ''; \
		echo '## Conventions'; \
		echo '- Name files `FEATURE-<feature>-<num>.md` where `<num>` is zero-padded and monotonically increasing.'; \
		echo '- Create one file per feature under `./active/` or `./archive/`.'; \
	} > $(FEATURES_INDEX)
	@printf '$(GREEN)✓ $(FEATURES_INDEX) updated$(RESET)\n'

# ── Maintenance ────────────────────────────────────────────────────────────────

.PHONY: tidy
tidy:
	@printf '$(CYAN)→ tidying modules...$(RESET)\n'
	@go mod tidy
	@printf '$(GREEN)✓ go.mod and go.sum updated$(RESET)\n'

.PHONY: clean
clean:
	@printf '$(CYAN)→ cleaning...$(RESET)\n'
	@rm -f $(BINARY) coverage.out coverage.html /tmp/wirecheck-bin
	@printf '$(GREEN)✓ clean$(RESET)\n'
