GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: all build test race cover lint fmt tidy bench release-snapshot clean

all: lint test build

build:
	$(GO) build -o starc ./cmd/starc

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

# Coverage across all packages, with an HTML report (coverage.html).
cover:
	$(GO) test -coverpkg=./... -coverprofile=coverage.out ./...
	./scripts/coverage.sh coverage.out

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...

tidy:
	$(GO) mod tidy

bench:
	$(GO) test -run '^$$' -bench . ./internal/compiler/

release-snapshot:
	$(GO) run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean

clean:
	rm -rf starc dist coverage.out coverage.html
