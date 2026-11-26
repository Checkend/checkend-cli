VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet

build: ## build bin/ce
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/ce ./cmd/ce

test:
	go test ./...

vet:
	go vet ./...
