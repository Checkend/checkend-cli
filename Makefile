.PHONY: check fmt lint test build e2e

# Run before each commit: format, vet, tests and build.
check: lint test build

fmt:
	gofmt -w .

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo "Run make fmt."; exit 1)
	go vet ./...

test:
	go test -race ./...

build:
	go build -o bin/ce ./cmd/ce

# The end-to-end test against a real server (see README.md).
e2e:
	go test -count=1 -run TestEndToEnd -v ./internal/cli/
