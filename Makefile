APP_NAME=shhh
APP_PACKAGE=github.com/rfizzle/shhh
GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
# Every check runs in one environment, whether a person types the target or
# the quality gate runs it under containment. Nothing inherits a provider
# route, credentials, terminal palette, or executable Git fsmonitor hook from
# the shell that launched it, and the tool caches live under TMPDIR — which a
# contained session already points at its private scratch space, so the gate
# neither needs permission to touch a shared cache nor takes a different
# branch because a developer happened to export a local setting.
HERMETIC_ENV=env -u SHHH_API_KEY -u SHHH_BASE_URL -u NO_COLOR GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0= TMPDIR=$${TMPDIR:-/tmp} XDG_CACHE_HOME=$${TMPDIR:-/tmp}/shhh-cache GOLANGCI_LINT_CACHE=$${TMPDIR:-/tmp}/shhh-golangci-lint
# The build tags the other test tiers live behind. vet reads them so a
# contract or integration file that stopped compiling fails here, in the
# gate, rather than in the one CI job that selects it.
TIER_TAGS=contract,integration
# gofmt ships with the toolchain but is not always on PATH — a Go installed
# through a version manager leaves it in GOROOT and nowhere else. Falling back
# to GOROOT is what keeps `make fmt` and the gofmt gate from quietly doing
# nothing on a machine where the binary is right there.
GOFMT ?= $(shell command -v gofmt 2>/dev/null || echo "$$(go env GOROOT)/bin/gofmt")
GOIMPORTS=goimports
GOLANGCI_LINT=golangci-lint
# `make fmt` rewrites files, so its list is a find rather than the index: a Go
# file that has been written but not yet added still has to be formatted. It
# skips dotted directories because a checkout can hold whole working trees
# underneath one — `git worktree` puts them wherever it is told, and a tool
# directory is a common place — and rewriting a file in somebody else's tree is
# precisely the stranger this list must not carry.
PROJECT_GOFILES=$(shell find . -type f -name '*.go' -not -path "./vendor/*" -not -path "./.*")
# The gate only reads, and what it reads is what a commit would carry, so it
# asks git rather than the filesystem. A nested working tree or a scratch file
# beside the source cannot then fail a build for the person who never wrote it.
TRACKED_GOFILES=$(shell git ls-files '*.go')
PROJECT_PACKAGES=$(shell go list ./...)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-s -w -X '$(APP_PACKAGE)/internal/cli.version=$(VERSION)'

ifneq (,$(findstring 256color, ${TERM}))
	RED     := $(shell tput -Txterm setaf 1)
	GREEN   := $(shell tput -Txterm setaf 2)
	YELLOW  := $(shell tput -Txterm setaf 3)
	CYAN    := $(shell tput -Txterm setaf 6)
	MAGENTA := $(shell tput -Txterm setaf 5)
	RESET   := $(shell tput -Txterm sgr0)
else
	RED     :=
	GREEN   :=
	YELLOW  :=
	CYAN    :=
	MAGENTA :=
	RESET   :=
endif

.PHONY: all build fmt fmt-check vet lint test test-contract test-integration docs docs-check cross ci eval eval-baseline cache-check model-data tui-shot tui-check tui-longpath help

all: help

## Build:
# One platform: the released matrix is goreleaser's (.goreleaser.yaml), and
# `make cross` is how a change is checked against it without building it.
build: fmt ## Build the binary for this platform
	@echo "${MAGENTA}Building $(APP_NAME)...${RESET}"
	@CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LDFLAGS)" -o $(APP_NAME) ./cmd/shhh

## Check:
fmt: ## Rewrite every Go file with gofmt and goimports
	@echo "${MAGENTA}Running gofmt...${RESET}"
	@$(GOFMT) -e -s -w $(PROJECT_GOFILES)
	@if command -v $(GOIMPORTS) >/dev/null 2>&1; then \
		echo "${MAGENTA}Running goimports...${RESET}"; \
		$(GOIMPORTS) -e -format-only -w -d $(PROJECT_GOFILES); \
	else \
		echo "${YELLOW}goimports not found, skipping (install: go install golang.org/x/tools/cmd/goimports@latest)${RESET}"; \
	fi

# The same two formatters `make fmt` runs, asked rather than told. A tree that
# is not clean at rest turns the next person's `make fmt` into a diff of
# somebody else's file, which they then either carry into their commit or spend
# a round reverting out of it — so the drift fails here instead, in the build
# that introduced it. Both checks are hard: unlike `make fmt`, this refuses to
# run when a formatter is missing, because a gate that skips itself reports the
# same green as a gate that passed.
fmt-check: ## Fail if any tracked Go file is not gofmt- and goimports-clean
	@echo "${MAGENTA}Checking gofmt...${RESET}"
	@command -v $(GOFMT) >/dev/null 2>&1 || { echo "${RED}gofmt not found at $(GOFMT) — it ships with the Go toolchain${RESET}"; exit 1; }
	@drift="$$($(GOFMT) -e -s -l $(TRACKED_GOFILES))"; \
	if [ -n "$$drift" ]; then \
		echo "${RED}Not gofmt-clean. Run make fmt:${RESET}"; \
		echo "$$drift"; \
		exit 1; \
	fi
	@echo "${MAGENTA}Checking goimports...${RESET}"
	@command -v $(GOIMPORTS) >/dev/null 2>&1 || { echo "${RED}goimports not found (install: go install golang.org/x/tools/cmd/goimports@latest)${RESET}"; exit 1; }
	@drift="$$($(GOIMPORTS) -e -format-only -l $(TRACKED_GOFILES))"; \
	if [ -n "$$drift" ]; then \
		echo "${RED}Imports are not grouped as goimports leaves them. Run make fmt:${RESET}"; \
		echo "$$drift"; \
		exit 1; \
	fi

vet: ## Run go vet over every tier's files
	@echo "${MAGENTA}Running go vet...${RESET}"
	@$(HERMETIC_ENV) $(GOVET) -mod=readonly -tags $(TIER_TAGS) $(PROJECT_PACKAGES)

lint: ## Run golangci-lint
	@echo "${MAGENTA}Running golangci-lint...${RESET}"
	@$(HERMETIC_ENV) $(GOLANGCI_LINT) run --modules-download-mode=readonly

## Test:
# The run stays cacheable on purpose: nothing here adds -count=1, and a test
# must not chdir (scripts/check-docs.py refuses one). A run that cannot be
# cached re-runs every package against a tree nothing has touched. The reader
# it runs through only reads the -json stream, and -json is a cacheable flag;
# it ends the output on a count per skip reason, which the gate carries.
test: ## Run the hermetic tier: every package, no listener, clipboard, daemon or network
	@echo "${MAGENTA}Running tests...${RESET}"
	@$(HERMETIC_ENV) $(GOCMD) run -mod=readonly ./scripts/gotest $(GOTEST) -mod=readonly -json $(PROJECT_PACKAGES)

test-contract: ## Run the loopback contract tier (needs a host that can bind a listener)
	@echo "${MAGENTA}Running loopback contract tests...${RESET}"
	@$(HERMETIC_ENV) $(GOTEST) -mod=readonly -tags=contract -count=1 -v ./internal/cli ./internal/eval ./internal/observe ./internal/reports

test-integration: ## Run the containment tier (needs the host's sandbox mechanism)
	@echo "${MAGENTA}Running sandbox integration checks...${RESET}"
	@$(HERMETIC_ENV) $(GOTEST) -mod=readonly -tags=integration -count=1 -v ./internal/sandbox

## Docs:
# The settings reference in docs/capabilities/configuration.md is written from
# the settings table rather than by hand: a default stated in prose and a
# default in the code are two places to be wrong, and the prose is the one
# that goes stale, because nothing fails when it does. The generator is a test
# so that it lives beside the table; running it without the variable is the
# staleness check, and `make ci` therefore performs it too.
docs: ## Rewrite the documentation sections generated from the code
	@echo "${MAGENTA}Writing the generated documentation sections...${RESET}"
	@SHHH_UPDATE_DOCS=1 $(GOTEST) -count=1 -run TestReference ./internal/config ./internal/ui/keys

docs-check: ## Verify every docs/ citation resolves and every generated section is current
	@echo "${MAGENTA}Checking documentation citations...${RESET}"
	@python3 scripts/check-docs.py
	@echo "${MAGENTA}Checking the generated documentation sections...${RESET}"
	@$(HERMETIC_ENV) $(GOTEST) -mod=readonly -count=1 -run TestReference ./internal/config ./internal/ui/keys

## Pipeline:
# The platforms goreleaser ships. A Unix-only syscall compiles perfectly on the
# machine that introduced it and breaks a release nobody builds until they tag
# one — which is how Windows was broken for four months. vet rather than build,
# because it covers the test files too, and those are where a platform symbol
# usually gets named first.
cross: ## Check every released platform still compiles
	@echo "${MAGENTA}Cross-compiling for every released platform...${RESET}"
	@for os in darwin linux windows; do \
		echo "  $$os"; \
		GOOS=$$os $(GOVET) -tags $(TIER_TAGS) $(PROJECT_PACKAGES) || exit 1; \
	done

# The CI pipeline, and nothing but the targets above in one order: the same
# spelling a person runs is the one the runner runs. The quality gate
# (.shhh/quality.json) is the first five; cross and the driven scenes are what
# CI adds, because a scene wants a terminal a contained session has not got.
ci: cross fmt-check docs-check test vet lint tui-check ## Run the CI pipeline

## Live:
# Costs real requests: ten of the fourteen cases put a task or a question to
# the model, which is several minutes and a few dollars a run, and more with
# --repeat. It is not part of `make ci` for that reason, and the workflow that
# runs it (.github/workflows/eval.yml) is one a person triggers. The four
# scripted cases cost nothing and run without an account at all, so
# `make eval EVAL_ARGS="--case close-gate"` is a free thing to ask for.
#
# Every run is read against evals/baseline.json; `make eval-baseline` is how
# that file is replaced, which is a commit somebody reviews.
eval: build ## Run the eval suite against the configured model (costs real requests)
	@echo "${MAGENTA}Running the eval suite...${RESET}"
	@./$(APP_NAME) eval $(EVAL_ARGS)

eval-baseline: build ## Rewrite evals/baseline.json from a fresh run (costs real requests)
	@echo "${MAGENTA}Refreshing the eval baseline...${RESET}"
	@./$(APP_NAME) eval --refresh-baseline $(EVAL_ARGS)

# The prompt-cache markers are the one thing the offline suite cannot judge: a
# marker the far end ignores looks exactly like one it honours, because the
# answer is identical and only the bill differs. So this asks two live
# endpoints — the Messages API directly, and a gateway forwarding to it, which
# is the path that can silently drop the field on the way through. Each check
# skips itself when its own variables are unset, so a run with one pair of
# credentials still checks that one. -count=1 because a cached PASS would be a
# run that asked nothing.
#
#	SHHH_CACHE_IT_URL=… SHHH_CACHE_IT_KEY=… \
#	SHHH_CACHE_IT_GATEWAY_URL=… SHHH_CACHE_IT_GATEWAY_KEY=… make cache-check
cache-check: ## Verify prompt caching against live endpoints (costs real requests)
	@echo "${MAGENTA}Checking prompt caching against the live endpoints...${RESET}"
	@$(GOTEST) -tags=integration -count=1 -v -run CacheIntegration ./internal/provider

model-data: ## Regenerate the built-in model-data snapshot from the public table
	@echo "${MAGENTA}Regenerating internal/pricing/models.json...${RESET}"
	@python3 scripts/model-data.py > internal/pricing/models.json

## TUI:
# The golden tests render a surface in-process. These drive the built binary
# in a tmux pane against a scripted model (scripts/tui/), which is the only
# way to see that a key reaches a surface and that the surface reaches the
# screen. A scene is a directory of replies and steps under
# scripts/tui/scenes/; a surface change adds a scene of its own, and every
# scene is run again by tui-check, so a scene is a test for as long as it is
# in the tree. Captures land under bin/tui/<scene>/ and are never committed:
# the scene is the record. To open a scene in this terminal instead:
#
#	SHHH_BIN=$PWD/bin/tui/shhh scripts/tui/drive.sh --attach scripts/tui/scenes/<name>
TUI_BIN=bin/tui/$(APP_NAME)
SCENES=$(sort $(dir $(wildcard scripts/tui/scenes/*/steps.txt)))
SCENE ?= smoke
define tui_build
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LDFLAGS)" -o $(TUI_BIN) ./cmd/shhh
endef

# The picture wants agg, which draws the captured cells and wants nothing
# else; a machine without it still gets the cells, and the recipe says so
# rather than failing.
tui-shot: ## Drive one scene and capture every step; with agg, draw them too (SCENE=<name> COLS=<width> ROWS=<height>)
	@$(tui_build)
	@echo "${MAGENTA}Driving the $(SCENE) scene...${RESET}"
	@if command -v agg >/dev/null 2>&1; then \
		SHHH_BIN=$(TUI_BIN) scripts/tui/drive.sh --pictures scripts/tui/scenes/$(SCENE); \
	else \
		echo "${YELLOW}agg not found — cells only, no pictures (brew install agg)${RESET}"; \
		SHHH_BIN=$(TUI_BIN) scripts/tui/drive.sh scripts/tui/scenes/$(SCENE); \
	fi

tui-check: ## Drive every scene through the built binary and fail on the first step that never draws what it waits for
	@$(tui_build)
	@for scene in $(SCENES); do \
		echo "${MAGENTA}Driving $$(basename $$scene)...${RESET}"; \
		SHHH_BIN=$(TUI_BIN) scripts/tui/drive.sh "$$scene" || exit 1; \
	done
	@$(MAKE) --no-print-directory tui-longpath

# A Unix socket's path is capped at 104 bytes, and a checkout under
# .claude/worktrees/<name>/ has already spent most of them. So the harness
# keeps its tmux socket under $$TMPDIR rather than beside the captures, and
# this drives one scene from a copy of scripts/tui/ whose own path is past
# the cap to hold it there: back under bin/tui/<scene>/ the run fails with
# "File name too long" and every snap times out, which is a failure nobody
# reads as a path being long. Part of tui-check, so the harness that lets an
# agent drive a scene from its worktree is itself tested on every change.
tui-longpath: ## Drive the smoke scene from a checkout path longer than a Unix socket's 104-byte cap
	@$(tui_build)
	@bin=$$PWD/$(TUI_BIN); \
	tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/shhh-longpath.XXXXXX") || exit 1; \
	deep=$$tmp; \
	while [ $${#deep} -lt 104 ]; do deep=$$deep/a-checkout-path-past-the-cap; done; \
	if ! mkdir -p "$$deep/scripts" || ! cp -R scripts/tui "$$deep/scripts/tui"; then \
		echo "${RED}Could not lay a checkout out under $$deep${RESET}"; rm -rf "$$tmp"; exit 1; \
	fi; \
	echo "${MAGENTA}Driving smoke from a $${#deep}-byte checkout...${RESET}"; \
	SHHH_BIN=$$bin "$$deep/scripts/tui/drive.sh" "$$deep/scripts/tui/scenes/smoke"; \
	status=$$?; rm -rf "$$tmp"; exit $$status

## Help:
help: ## Show this help
	@echo ''
	@echo 'Usage:'
	@echo '  ${YELLOW}make${RESET} ${GREEN}<target>${RESET}'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} { \
		if (/^[a-zA-Z_-]+:.*?##.*$$/) {printf "    ${YELLOW}%-20s${GREEN}%s${RESET}\n", $$1, $$2} \
		else if (/^## .*$$/) {printf "  ${CYAN}%s${RESET}\n", substr($$1,4)} \
		}' $(MAKEFILE_LIST)
