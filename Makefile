BINARY  := tmuxctl
GOBIN   := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

.PHONY: build install vet clean

build:
	go build -o $(BINARY) .

install:
	go install .
	@echo "installed $(GOBIN)/$(BINARY)"

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
