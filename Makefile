BINARY  := tmuxctl
GOBIN   := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

.PHONY: build install test vet clean help
.DEFAULT_GOAL := help

build: ## build ./tmuxctl
	go build -o $(BINARY) .

install: ## go install into GOBIN (default ~/go/bin)
	go install .
	@echo "installed $(GOBIN)/$(BINARY)"

test: ## run the tests
	go test ./...

vet: ## run go vet
	go vet ./...

clean: ## remove the built binary
	rm -f $(BINARY)

help: ## list available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
