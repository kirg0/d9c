BINARY := d9c
VERSION ?= dev
LDFLAGS := -ldflags "-X main.version=$(VERSION) -s -w"

.PHONY: build run demo test race cover fmt fmtcheck vet staticcheck lint check tidy clean tools

build:
	go build $(LDFLAGS) -o $(BINARY) .

run:
	go run . $(ARGS)

# Launch the TUI against built-in sample data (no Docker needed).
demo:
	go run . -demo

test:
	go test ./...

# Tests with the race detector enabled.
race:
	go test -race ./...

# Coverage profile plus a browsable HTML report (coverage.html) and the total.
# CI publishes the same report to https://kirg0.github.io/d9c/coverage/.
cover:
	go test -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out | tail -n 1

# Rewrite files to canonical gofmt form.
fmt:
	gofmt -w .

# Fail if any file is not gofmt-clean (for CI / quality gate).
fmtcheck:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

vet:
	go vet ./...

staticcheck:
	staticcheck ./...

# Full multi-linter pass (config in .golangci.yml).
lint:
	golangci-lint run ./...

# Full quality gate. Run this before considering a change done.
check: fmtcheck vet lint test

tidy:
	go mod tidy

# Install the dev tooling used by `make check` / `make lint`.
tools:
	go install honnef.co/go/tools/cmd/staticcheck@latest
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

clean:
	rm -f $(BINARY)
