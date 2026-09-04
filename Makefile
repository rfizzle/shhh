APP_NAME=shhh
APP_PACKAGE=github.com/rfizzle/shhh
GOCMD=go
GOMOD=$(GOCMD) mod
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
# gofmt ships with the toolchain but is not always on PATH — a Go installed
# through a version manager leaves it in GOROOT and nowhere else. Falling back
# to GOROOT is what keeps `make fmt` and the gofmt gate from quietly doing
# nothing on a machine where the binary is right there.
GOFMT ?= $(shell command -v gofmt 2>/dev/null || echo "$$(go env GOROOT)/bin/gofmt")
GOIMPORTS=goimports
GOLANGCI_LINT=golangci-lint
PROJECT_GOFILES=$(shell find . -type f -name '*.go' -not -path "./vendor/*")
PROJECT_PACKAGES=$(shell go list ./...)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-list -1 HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS=-s -w -X '$(APP_PACKAGE)/internal/cli.version=$(VERSION)'

ifneq (,$(findstring 256color, ${TERM}))
	RED     := $(shell tput -Txterm setaf 1)
	GREEN   := $(shell tput -Txterm setaf 2)
	YELLOW  := $(shell tput -Txterm setaf 3)
	BLUE    := $(shell tput -Txterm setaf 4)
	MAGENTA := $(shell tput -Txterm setaf 5)
	CYAN    := $(shell tput -Txterm setaf 6)
	WHITE   := $(shell tput -Txterm setaf 7)
	RESET   := $(shell tput -Txterm sgr0)
else
	RED     := ""
	GREEN   := ""
	YELLOW  := ""
	BLUE    := ""
	MAGENTA := ""
	CYAN    := ""
	WHITE   := ""
	RESET   := ""
endif

.PHONY: all build build-all linux darwin windows clean fmt lint tidy test race ci cross docs docs-check eval help

all: help

## Build:
build: fmt tidy ## Build binary for current platform
	@echo "${MAGENTA}Building $(APP_NAME)...${RESET}"
	@CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LDFLAGS)" -o $(APP_NAME) ./cmd/shhh

build-all: fmt clean tidy darwin linux windows ## Build for all platforms
	@echo "${MAGENTA}Finished building all platforms.${RESET}"

darwin: ## Build for macOS (amd64 + arm64)
	@echo "${MAGENTA}Building for macOS amd64...${RESET}"
	@CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOCMD) build -ldflags "$(LDFLAGS)" -o bin/darwin-amd64/$(APP_NAME) ./cmd/shhh
	@echo "${MAGENTA}Building for macOS arm64...${RESET}"
	@CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOCMD) build -ldflags "$(LDFLAGS)" -o bin/darwin-arm64/$(APP_NAME) ./cmd/shhh

linux: ## Build for Linux (amd64 + arm64)
	@echo "${MAGENTA}Building for Linux amd64...${RESET}"
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOCMD) build -ldflags "$(LDFLAGS)" -o bin/linux-amd64/$(APP_NAME) ./cmd/shhh
	@echo "${MAGENTA}Building for Linux arm64...${RESET}"
	@CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOCMD) build -ldflags "$(LDFLAGS)" -o bin/linux-arm64/$(APP_NAME) ./cmd/shhh

windows: ## Build for Windows (amd64 + arm64)
	@echo "${MAGENTA}Building for Windows amd64...${RESET}"
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOCMD) build -ldflags "$(LDFLAGS)" -o bin/windows-amd64/$(APP_NAME).exe ./cmd/shhh
	@echo "${MAGENTA}Building for Windows arm64...${RESET}"
	@CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GOCMD) build -ldflags "$(LDFLAGS)" -o bin/windows-arm64/$(APP_NAME).exe ./cmd/shhh

clean: ## Remove build artifacts
	@echo "${MAGENTA}Cleaning build artifacts...${RESET}"
	@rm -rf ./bin

## Format:
fmt: ## Run gofmt and goimports on all source files
	@echo "${MAGENTA}Running gofmt...${RESET}"
	@$(GOFMT) -e -s -w $(PROJECT_GOFILES)
	@if command -v $(GOIMPORTS) >/dev/null 2>&1; then \
		echo "${MAGENTA}Running goimports...${RESET}"; \
		$(GOIMPORTS) -e -format-only -w -d $(PROJECT_GOFILES); \
	else \
		echo "${YELLOW}goimports not found, skipping (install: go install golang.org/x/tools/cmd/goimports@latest)${RESET}"; \
	fi

lint: ## Run go vet and golangci-lint
	@echo "${MAGENTA}Running go vet...${RESET}"
	@$(GOVET) $(PROJECT_PACKAGES)
	@echo "${MAGENTA}Running golangci-lint...${RESET}"
	@$(GOLANGCI_LINT) run

## Dependencies:
tidy: ## Tidy go.mod
	@echo "${MAGENTA}Tidying go.mod...${RESET}"
	@$(GOMOD) tidy

## Data:
model-data: ## Regenerate the built-in model-data snapshot from the public table
	@echo "${MAGENTA}Regenerating internal/pricing/models.json...${RESET}"
	@python3 scripts/model-data.py > internal/pricing/models.json

## Docs:
# The settings reference in docs/capabilities/configuration.md is written from
# the settings table rather than by hand: a default stated in prose and a
# default in the code are two places to be wrong, and the prose is the one
# that goes stale, because nothing fails when it does. The generator is a test
# so that it lives beside the table; running it without the variable is the
# staleness check, and `make ci` therefore performs it too.
docs: ## Rewrite the documentation sections generated from the code
	@echo "${MAGENTA}Writing the generated documentation sections...${RESET}"
	@SHHH_UPDATE_DOCS=1 $(GOTEST) -count=1 -run TestReference ./internal/config

docs-check: ## Verify every docs/ citation resolves and every generated section is current
	@echo "${MAGENTA}Checking documentation citations...${RESET}"
	@python3 scripts/check-docs.py
	@echo "${MAGENTA}Checking the generated documentation sections...${RESET}"
	@$(GOTEST) -count=1 -run TestReference ./internal/config

## Evals:
eval: build ## Run the eval suite against the configured model (costs real requests)
	@echo "${MAGENTA}Running the eval suite...${RESET}"
	@./$(APP_NAME) eval $(EVAL_ARGS)

## Cross:
# The platforms goreleaser ships. A Unix-only syscall compiles perfectly on the
# machine that introduced it and breaks a release nobody builds until they tag
# one — which is how Windows was broken for four months. vet rather than build,
# because it covers the test files too, and those are where a platform symbol
# usually gets named first.
cross: ## Check every released platform still compiles
	@echo "${MAGENTA}Cross-compiling for every released platform...${RESET}"
	@for os in darwin linux windows; do \
		echo "  $$os"; \
		GOOS=$$os $(GOVET) $(PROJECT_PACKAGES) || exit 1; \
	done

## Test:
test: ## Run tests
	@echo "${MAGENTA}Running tests...${RESET}"
	@$(GOTEST) -v $(PROJECT_PACKAGES)

race: ## Run tests with race detector
	@echo "${MAGENTA}Running tests with race detector...${RESET}"
	@$(GOTEST) -v -race $(PROJECT_PACKAGES)

# The test run stays cacheable on purpose: -v and -failfast are both flags
# `go test` will still match a cached result against, and nothing here adds
# -count=1. It reads like a detail and is not — the CLI suite links the binary
# its print-mode tests drive, so a run that cannot be cached re-links it and
# re-runs every package against a tree nothing has touched.
ci: cross ## Run tests and lint for CI
	@echo "${MAGENTA}Checking documentation citations...${RESET}"
	@python3 scripts/check-docs.py
	@echo "${MAGENTA}Running tests...${RESET}"
	@$(GOTEST) -v -failfast $(PROJECT_PACKAGES)
	@echo "${MAGENTA}Running gofmt check...${RESET}"
	@command -v $(GOFMT) >/dev/null 2>&1 || { echo "${RED}gofmt not found at $(GOFMT) — it ships with the Go toolchain${RESET}"; exit 1; }
	@unformatted="$$($(GOFMT) -e -s -l $(PROJECT_GOFILES))"; \
	if [ -n "$$unformatted" ]; then \
		echo "${RED}Not gofmt-clean. Run make fmt:${RESET}"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "${MAGENTA}Running golangci-lint...${RESET}"
	@$(GOLANGCI_LINT) run

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
