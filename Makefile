# ESFS build/test/package Makefile.
SHELL := /bin/sh
VERSION ?= 0.1.0-dev
LDFLAGS := -s -w -X github.com/dbmurphy/elasticsearch-filesystem/internal/cli.Version=$(VERSION)
BINDIR := bin
CMDS := esfs esfsd esfs-grep esfs-ls esfs-find

.PHONY: all build test vet fmt bench clean cross install uninstall tidy acceptance

all: build

build: ## build all binaries for the host platform
	@mkdir -p $(BINDIR)
	@for c in $(CMDS); do \
		echo "build $$c"; \
		go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$c ./cmd/$$c || exit 1; \
	done

test: ## run all tests
	go test ./...

vet: ## run go vet
	go vet ./...

fmt: ## gofmt all sources
	gofmt -w internal cmd

bench: ## run ESFS-overhead benchmarks
	go test -run '^$$' -bench . -benchmem ./internal/contract/ ./internal/escore/

acceptance: ## run Docker-based Linux + real-Elasticsearch acceptance loop
	./test/docker/run.sh

tidy:
	go mod tidy

clean:
	rm -rf $(BINDIR) dist

# Cross-compile the release matrix. go-fuse compiles on linux and darwin.
cross: ## build release binaries for linux/darwin amd64/arm64
	@mkdir -p dist
	@for osarch in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
		os=$${osarch%/*}; arch=$${osarch#*/}; \
		out=dist/esfs-$(VERSION)-$$os-$$arch; mkdir -p $$out; \
		for c in $(CMDS); do \
			echo "build $$c ($$os/$$arch)"; \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $$out/$$c ./cmd/$$c || exit 1; \
		done; \
		tar -C dist -czf $$out.tar.gz $$(basename $$out); \
	done
	@echo "release artifacts in dist/"

install: build ## install binaries to PREFIX (default /usr/local)
	@PREFIX=$${PREFIX:-/usr/local}; \
	install -d $$PREFIX/bin; \
	for c in $(CMDS); do install -m 0755 $(BINDIR)/$$c $$PREFIX/bin/$$c; done; \
	echo "installed $(CMDS) to $$PREFIX/bin"

uninstall:
	@PREFIX=$${PREFIX:-/usr/local}; \
	for c in $(CMDS); do rm -f $$PREFIX/bin/$$c; done; \
	echo "removed $(CMDS) from $$PREFIX/bin"
