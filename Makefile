# Pacenote plugin interface — developer entry points. Every target is what CI runs.
SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
export PATH := $(PATH):$(shell go env GOPATH)/bin

# The tools, each named once.
#
# The PATH above is not enough on its own. GNU Make runs a recipe line directly,
# without a shell, when the line holds no shell metacharacters — and that direct
# execution searches make's own PATH rather than the one exported here. A tool
# installed by `go install` is invisible to exactly the recipes that are a single
# command, and the error says "No such file or directory" rather than anything
# about PATH. `make lint` failed that way while `make fmt-check`, which happens
# to use a shell, worked.
GOBIN        := $(shell go env GOPATH)/bin
GOFUMPT      := $(shell command -v gofumpt       2>/dev/null || echo $(GOBIN)/gofumpt)
GOLANGCILINT := $(shell command -v golangci-lint 2>/dev/null || echo $(GOBIN)/golangci-lint)
BUF          := $(shell command -v buf           2>/dev/null || echo $(GOBIN)/buf)

TESTFLAGS := -race -shuffle=on -count=1
COVER_MIN := 90

.DEFAULT_GOAL := check

.PHONY: help check build vet lint fmt fmt-check test cover bench bench-smoke proto tidy-check clean

## help: list targets
help:
	@grep -E '^## [a-z-]+:' $(MAKEFILE_LIST) | sed -E 's/^## ([a-z-]+): */\1\t/' | column -t -s $$'\t'

## check: everything CI runs, in order
check: fmt-check build vet lint test bench-smoke tidy-check

## build: compile the module and the example plugin
build:
	go build ./...

## vet: go vet
vet:
	go vet ./...

## lint: golangci-lint
lint:
	$(GOLANGCILINT) run ./...

## fmt: gofumpt in place
fmt:
	$(GOFUMPT) -w .

## fmt-check: fail if anything is unformatted
fmt-check:
	@out=$$($(GOFUMPT) -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

## test: the whole suite, with the race detector
test:
	go test $(TESTFLAGS) ./...

## bench: the transport benchmarks
bench:
	go test -run XXX -bench . -benchmem ./...

## bench-smoke: run each benchmark once, so they cannot rot unnoticed
bench-smoke:
	go test -run XXX -bench . -benchtime=1x ./...

## cover: coverage — every package over $(COVER_MIN)%
cover:
	go test -covermode=atomic -coverprofile=coverage.out ./...
	scripts/coverage.sh coverage.out $(COVER_MIN)

## proto: regenerate internal/pb from proto/plugin.proto
proto:
	$(BUF) generate

## tidy-check: fail if go.mod or go.sum would change
tidy-check:
	@cp go.mod go.mod.bak && cp go.sum go.sum.bak
	@go mod tidy
	@if ! diff -q go.mod go.mod.bak >/dev/null || ! diff -q go.sum go.sum.bak >/dev/null; then \
		mv go.mod.bak go.mod; mv go.sum.bak go.sum; echo "go mod tidy would change go.mod or go.sum"; exit 1; fi
	@rm -f go.mod.bak go.sum.bak

## clean: remove build outputs
clean:
	rm -f coverage.out
