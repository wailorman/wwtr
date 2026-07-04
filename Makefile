# Makefile for wwtr. Standard targets: build, test, lint, fmt, vuln, install.
# Lint and test are the project's main quality gates (see PLAN §19, §21).

SHELL := /bin/sh

GO          ?= go
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
COVER_MIN   := 95
PKGS        := ./...
# Packages counted toward the coverage gate. Root main.go and internal/di
# (OS wrappers exercised only via fakes in app tests) are excluded by policy.
COVER_PKGS  := $(shell $(GO) list ./... | grep -vE 'github.com/wailorman/wwtr$$|github.com/wailorman/wwtr/internal/di$$')

.PHONY: all build install test test-race test-cover cover lint fmt vuln clean tidy

all: build test lint

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/wwtr .

install:
	$(GO) install -ldflags "$(LDFLAGS)" .

test:
	$(GO) test $(PKGS)

test-race:
	$(GO) test -race $(PKGS)

test-cover:
	$(GO) test -race -coverprofile=cover.out -covermode=atomic $(COVER_PKGS)
	@$(GO) tool cover -func=cover.out | tail -1
	@cov=$$( $(GO) tool cover -func=cover.out | tail -1 | awk '{print $$NF}' | tr -d '%' ); \
	  if [ $${cov%.*} -lt $(COVER_MIN) ]; then \
	    echo "coverage $$cov% < $(COVER_MIN)% required"; exit 1; \
	  else echo "coverage $$cov% OK (>= $(COVER_MIN)%)"; fi

cover: test-cover
	$(GO) tool cover -html=cover.out

# golangci-lint v2 reads config from .golangci.yml.
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint not found; install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; exit 1; }
	golangci-lint run

fmt:
	@command -v gofumpt >/dev/null 2>&1 && gofumpt -w . || $(GO) fmt ./...

vuln:
	$(GO) tool govulncheck ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin/ cover.out
