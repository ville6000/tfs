BINARY  := tfs
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help build install test lint fmt tidy clean

help: ## Show this help
	@grep -E '^[a-z]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

build: ## Build ./bin/tfs
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/tfs

install: ## Install tfs into $GOBIN
	go install -ldflags "$(LDFLAGS)" ./cmd/tfs

test: ## Run tests
	go test -race ./...

lint: ## Run go vet and golangci-lint
	go vet ./...
	golangci-lint run

fmt: ## Format code
	gofmt -w .

tidy: ## Tidy go.mod
	go mod tidy

clean: ## Remove build output
	rm -rf bin dist
