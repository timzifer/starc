GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: all build test race cover lint fmt tidy bench release-snapshot clean js-wasm js-test site

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
	rm -rf starc dist coverage.out coverage.html coverage-badge.json site js/testdata/starc.wasm

# The compiler for JavaScript (cmd/starc-js) and Go's loader for it.
GOROOT_WASM = $(shell $(GO) env GOROOT)/lib/wasm

js-wasm:
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -o js/testdata/starc.wasm ./cmd/starc-js

# Parity of the JavaScript compiler and host with the Go ones (needs Node.js).
js-test: js-wasm
	$(GO) run ./internal/cmd/jsfixtures > js/testdata/parity.json
	STARC_WASM_EXEC=$(GOROOT_WASM)/wasm_exec.js node --test js/test/host.test.mjs js/test/parity.test.mjs

# The playground (web/) as a static site in site/; serve it with any web
# server, for example "python3 -m http.server -d site".
site: js-wasm
	rm -rf site && mkdir -p site
	cp web/* js/host.mjs js/compiler.mjs js/testdata/starc.wasm site/
	cp $(GOROOT_WASM)/wasm_exec.js site/
