SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
NPM ?= npm
BINARY := bin/cloudmanager
VERSION ?= dev
COMMIT ?= unknown
BUILD_TIME ?= unknown
LDFLAGS := -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.Version=$(VERSION) -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.Commit=$(COMMIT) -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.BuildTime=$(BUILD_TIME)

.PHONY: help init build build-go build-web serve worker web-dev migrate admin keygen version test verify go-test typecheck fmt-check vet scripts-check release docker-init docker-up docker-down docker-logs docker-config install

help:
	@echo "Cloud Account Manager"
	@echo "  make init          Initialize the local PostgreSQL 18 development environment"
	@echo "  make build         Build the React frontend and Go binary"
	@echo "  make serve         Start API and embedded worker on 127.0.0.1:8080"
	@echo "  make web-dev       Start the Vite development server on 127.0.0.1:5173"
	@echo "  make worker        Start a standalone worker"
	@echo "  make test          Run backend tests and frontend type checking"
	@echo "  make verify        Run the complete local quality gate"
	@echo "  make release       Build Linux release archives (set VERSION=vX.Y.Z)"
	@echo "  make docker-init   Initialize the PostgreSQL 18 Docker database"
	@echo "  make docker-up     Build and start the Docker Compose stack"
	@echo "  make install       Install production services on Ubuntu/Debian"

init:
	./scripts/dev-init.sh

build: build-web build-go

build-go:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/cloudmanager

build-web:
	cd web && if [[ -f package-lock.json ]]; then $(NPM) ci; else $(NPM) install; fi
	cd web && $(NPM) run build

serve:
	$(GO) run ./cmd/cloudmanager serve

worker:
	RUN_WORKER=false $(GO) run ./cmd/cloudmanager worker

web-dev:
	cd web && $(NPM) run dev

migrate:
	$(GO) run ./cmd/cloudmanager migrate

admin: build-go
	$(BINARY) admin

keygen:
	$(GO) run ./cmd/cloudmanager keygen

version: build-go
	$(BINARY) version

test: go-test typecheck fmt-check

verify: test vet build scripts-check docker-config

go-test:
	$(GO) test ./...

typecheck:
	cd web && $(NPM) run typecheck

fmt-check:
	@files="$$(gofmt -l cmd internal)"; if [[ -n "$$files" ]]; then echo "Go files require gofmt:"; echo "$$files"; exit 1; fi

vet:
	$(GO) vet ./...

scripts-check:
	bash -n scripts/dev-init.sh scripts/install.sh scripts/deploy-compose.sh scripts/build-release.sh

release:
	./scripts/build-release.sh "$(VERSION)"

docker-init:
	./scripts/dev-init.sh --docker

docker-up:
	@if [[ ! -f .env.local ]]; then ./scripts/dev-init.sh --env-only; fi
	docker compose --env-file .env.local up -d --build

docker-down:
	docker compose --env-file .env.local down

docker-logs:
	docker compose --env-file .env.local logs -f api worker

docker-config:
	@if [[ ! -f .env.local ]]; then ./scripts/dev-init.sh --env-only; fi
	docker compose --env-file .env.local config --quiet

install:
	sudo ./scripts/install.sh
