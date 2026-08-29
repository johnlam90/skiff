# =============================================================================
# File: Makefile
# Author: Spicer Matthews <spicer@cloudmanic.com>
# Created: 2026-04-29
# Copyright: 2026 Cloudmanic, LLC. All rights reserved.
# =============================================================================

BINARY := skiff
SITE_DIR := website

# Shipped-binary build flags, kept in one place so `make build`,
# `make build-linux`, and .goreleaser.yml cannot drift: a binary you build
# locally is the same shape as the one users download. CGO stays off
# (the whole point is one static binary) and the symbol table is stripped.
# Note this is NOT exported globally — `go test -race` needs cgo.
GO_LDFLAGS := -s -w
GO_BUILD := CGO_ENABLED=0 go build -ldflags='$(GO_LDFLAGS)'

.PHONY: run build install build-linux test test-short lint coverage tidy clean help \
        site-install site-dev site-build site-clean

# help is the default target so `make` with no args prints what's available.
help:
	@echo "Skiff — opinionated mouse-first terminal code editor"
	@echo ""
	@echo "Editor targets:"
	@echo "  make run          Run the editor in the current directory."
	@echo "  make build        Build the binary into ./bin/$(BINARY)."
	@echo "  make install      Install ./bin/$(BINARY) into /usr/local/bin."
	@echo "  make build-linux  Cross-compile a static linux/amd64 binary."
	@echo "  make test         Run the full suite with -race."
	@echo "  make lint         gofmt + go vet + staticcheck, same as CI."
	@echo "  make test-short   Skip slow tests (-short) — quick iteration loop."
	@echo "  make coverage     Generate coverage.out + an HTML report at coverage.html."
	@echo "  make tidy         Run 'go mod tidy'."
	@echo "  make clean        Remove ./bin and coverage artifacts."
	@echo ""
	@echo "Website targets (johnlam90.github.io/skiff — Hugo + Tailwind in ./$(SITE_DIR)):"
	@echo "  make site-install One-time: install npm deps in $(SITE_DIR)."
	@echo "  make site-dev     Run the site locally with live reload at http://localhost:1313."
	@echo "  make site-build   Build a production-ready site into $(SITE_DIR)/public."
	@echo "  make site-clean   Remove $(SITE_DIR)/public and Tailwind output."

# run starts the editor via 'go run'. Quickest path for development.
# For SSH/production use, prefer 'make build' and ship the binary.
run:
	go run .

# build produces a single binary at ./bin/$(BINARY), using the exact flags
# the release ships with (see GO_BUILD above).
build:
	mkdir -p bin
	$(GO_BUILD) -o bin/$(BINARY) .

# install copies the binary into /usr/local/bin so you can launch it as `skiff`.
install: build
	install -m 0755 bin/$(BINARY) /usr/local/bin/$(BINARY)

# build-linux cross-compiles a fully static linux/amd64 binary. Drop the
# resulting bin/$(BINARY)-linux-amd64 onto a remote box and run it inside
# tmux/zellij — no runtime, no libc, just one file.
build-linux:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 $(GO_BUILD) -o bin/$(BINARY)-linux-amd64 .

# test runs the full suite with the race detector. The same command
# CI runs (.github/workflows/test.yml) — keep them in lockstep so a
# green CI is the same signal as a green local run.
test:
	go test -race ./...

# test-short is the quick local iteration loop: skip anything tagged
# slow with -short, no race detector. Use this while writing tests.
test-short:
	go test -short ./...

# lint runs the same three gates CI enforces, in the same order, so a
# clean local run means a clean pipeline. STATICCHECK_VERSION is pinned to
# the tag .github/workflows/test.yml installs — bump both together.
#
# gofmt is VERSION-SENSITIVE: newer toolchains reflow doc comments the
# older one leaves alone, and vice versa. CI resolves Go from the go
# directive in go.mod, so a newer local toolchain can call a file clean
# that CI rejects. If lint passes here and fails in CI, reproduce with
# that exact version: go install golang.org/dl/go1.24.0@latest &&
# go1.24.0 download && go1.24.0 fmt ./...
STATICCHECK_VERSION := v0.6.1
lint:
	@unformatted="$$(gofmt -l . | grep -v '^website/' || true)"; \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

# coverage produces a coverage profile across every package and a
# rendered HTML report you can open in a browser.
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"
	@go tool cover -func=coverage.out | tail -n 1

# tidy keeps go.mod / go.sum in sync with what's actually imported.
tidy:
	go mod tidy

# clean removes build artifacts and coverage output.
clean:
	rm -rf bin coverage.out coverage.html

# -----------------------------------------------------------------------------
# Website targets — johnlam90.github.io/skiff lives in ./website (Hugo + Tailwind v4).
# Requires Hugo extended (>= 0.135) and Node (>= 18) on PATH.
# -----------------------------------------------------------------------------

# site-install pulls the npm deps the site needs (Tailwind CLI + npm-run-all).
# Idempotent — safe to re-run any time.
site-install:
	cd $(SITE_DIR) && npm install

# site-dev runs Tailwind in watch mode and Hugo's dev server in parallel,
# so edits to layouts, content, or CSS rebuild and live-reload at
# http://localhost:1313. Stops both on Ctrl+C.
site-dev:
	cd $(SITE_DIR) && npm run dev

# site-build produces the production-ready static site at $(SITE_DIR)/public.
# This is what the Pages deploy (.github/workflows/pages.yml) builds and
# ships — same command there as here, so local and CI builds can't drift.
# The Tailwind build runs first so the minified CSS is on disk before Hugo
# reads its static directory.
site-build:
	cd $(SITE_DIR) && npm run build

# site-clean removes the generated build outputs. The npm cache and
# node_modules stay put — that's site-install's job to manage.
site-clean:
	rm -rf $(SITE_DIR)/public $(SITE_DIR)/static/css/site.css $(SITE_DIR)/resources
