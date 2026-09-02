# Common tasks. `make install` puts kancli on your PATH so it runs from any
# directory; the board itself always lives in your data directory, never in
# the directory you happen to be in.

BIN     := kancli
PKG     := ./cmd/kancli
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= $(HOME)/.local
BINDIR  := $(PREFIX)/bin

.PHONY: build install uninstall run demo test lint clean release-snapshot help

build:            ## build ./kancli
	go build -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

install: build    ## copy the binary to ~/.local/bin (override with PREFIX=)
	mkdir -p $(BINDIR)
	cp $(BIN) $(BINDIR)/$(BIN)
	@echo "installed $(BINDIR)/$(BIN)"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "note: add $(BINDIR) to your PATH, e.g. export PATH=\"$(BINDIR):\$$PATH\"";; esac

uninstall:        ## remove the installed binary (your board data is kept)
	rm -f $(BINDIR)/$(BIN)

run: build        ## build and open your board
	./$(BIN)

demo: build       ## build and open the sample board
	./$(BIN) -demo

test:             ## run the tests with the race detector
	go test -race ./...

lint:             ## gofmt, vet and golangci-lint
	@test -z "$$(gofmt -l ./cmd ./internal)" || { gofmt -l ./cmd ./internal; echo "run gofmt -w ."; exit 1; }
	go vet ./...
	golangci-lint run ./...

release-snapshot: ## build release archives locally without publishing
	goreleaser release --snapshot --clean

clean:            ## remove build output
	rm -rf $(BIN) $(BIN).exe dist

help:             ## show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'
