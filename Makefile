#!/usr/bin/make
SHELL = /bin/sh

# Docker Compose Configuration
DC_BASE_ARGS = --rm --user "$(shell id -u):$(shell id -g)" --no-deps
DC_GO_RUN = docker-compose run $(DC_BASE_ARGS) go
DC_LINT_RUN = docker-compose run --rm --no-deps golint

.PHONY : help fmt vet lint gotest test build cover tidy shell clean
.DEFAULT_GOAL : help
.SILENT : lint gotest

# Help and Documentation
help: ## Show this help
	@printf "\033[33m%s:\033[0m\n" 'Available commands'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[32m%-11s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Code Formatting
fmt: ## Run source code formatter tools
	$(DC_GO_RUN) sh -c 'go install golang.org/x/tools/cmd/goimports@latest && $$GOPATH/bin/goimports -d -w .'
	$(DC_GO_RUN) gofmt -s -w -d .
	$(DC_GO_RUN) go mod tidy

# Code Quality
vet: ## Run go vet
	$(DC_GO_RUN) go vet ./...

lint: ## Run go linters
	$(DC_LINT_RUN) golangci-lint run

# Testing
gotest: ## Run go tests with race detector and coverage
	docker-compose run $(DC_BASE_ARGS) -e CGO_ENABLED=1 go \
		go test -v -race -timeout 30s -coverprofile=coverage.out ./...

test: lint gotest ## Run go tests and linters

cover: gotest ## Generate HTML coverage report
	$(DC_GO_RUN) go tool cover -html=coverage.out -o coverage.html

# Build
build: ## Build the package (compile check)
	$(DC_GO_RUN) go build ./...

# Dependencies
tidy: ## Tidy go.mod/go.sum
	$(DC_GO_RUN) go mod tidy

# Development Tools
shell: ## Start shell into container with golang
	$(DC_GO_RUN) bash

# Cleanup
clean: ## Stop containers, remove volumes and coverage artifacts
	docker-compose down -v -t 1
	rm -f coverage.out coverage.html
