SHELL := /bin/sh
COMPOSE := docker compose
GO ?= go

.PHONY: help fmt lint web-install web-build test test-unit test-race test-integration test-ui test-stale test-e2e test-chaos test-multiarch test-all build bootstrap up down reset logs demo inspect

help:
	@printf '%s\n' 'Continuity Lab targets:' \
	  '  bootstrap         build and start the complete local cluster' \
	  '  demo              run the real Git demonstration' \
	  '  test              run Go tests' \
	  '  test-unit         run unit tests' \
	  '  test-integration  run real MinIO/Git integration tests' \
	  '  test-ui           type-check and build the React interface' \
	  '  test-e2e          run Smart HTTP end-to-end tests' \
	  '  test-chaos        run failpoint and recovery tests' \
	  '  test-all          run lint, unit, integration, e2e, and chaos' \
	  '  reset             stop cluster and delete lab data'

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

test: test-unit

test-unit:
	$(GO) test -count=1 ./internal/...

test-race:
	$(GO) test -race -count=1 ./internal/...

web-install:
	npm ci --prefix web

web-build: web-install
	npm run build --prefix web

test-ui: web-build
	./scripts/test-ui.sh

# Integration package skips only when explicitly run without its required environment.
test-integration:
	CONTINUITY_INTEGRATION=1 $(GO) test -count=1 ./tests/integration/...

test-stale:
	./scripts/test-stale-reads.sh

test-e2e:
	./scripts/test-e2e.sh

test-chaos:
	./scripts/test-chaos.sh

test-multiarch:
	./scripts/test-multiarch.sh

test-all: lint test-unit test-race test-integration test-ui test-multiarch test-stale test-e2e test-chaos

build: web-build
	@mkdir -p bin
	$(GO) build -o bin/gateway ./cmd/gateway
	$(GO) build -o bin/node ./cmd/node
	$(GO) build -o bin/continuityctl ./cmd/continuityctl
	$(GO) build -o bin/continuity-hook ./cmd/continuity-hook

bootstrap: build
	./scripts/bootstrap.sh

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

reset:
	./scripts/reset.sh

logs:
	$(COMPOSE) logs -f --tail=200

demo:
	./scripts/demo.sh

inspect:
	./bin/continuityctl cluster status
