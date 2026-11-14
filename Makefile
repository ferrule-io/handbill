# handbill developer tasks. Tool versions come from mise.toml (`mise install`);
# dev environment variables come from mise.toml's [env].

WEB_DIST := internal/webui/dist
IMAGE    ?= handbill:dev
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -X github.com/ferrule-io/handbill/internal/version.Version=$(VERSION) \
            -X github.com/ferrule-io/handbill/internal/version.Commit=$(COMMIT)

.PHONY: all web build run db-up db-down test vet lint web-lint web-typecheck e2e docker clean kind-smoke

all: web build

## web: build the Next.js export and copy it where internal/webui embeds it
web:
	cd web && pnpm install --frozen-lockfile && pnpm build
	find $(WEB_DIST) -mindepth 1 ! -name .gitkeep -delete
	cp -R web/out/. $(WEB_DIST)/

## build: compile the server (embeds whatever is in internal/webui/dist)
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/handbill ./cmd/handbill

## run: build everything and start the server against the Compose database
run: web build db-up
	./bin/handbill

db-up:
	docker compose up -d --wait postgres postgres-test

db-down:
	docker compose down

vet:
	go vet ./...

test:
	go test ./...

web-lint:
	cd web && pnpm lint

web-typecheck:
	cd web && pnpm typecheck

lint: vet web-lint web-typecheck

## e2e: Playwright acceptance suite against a running server (BASE_URL). Run it
## with the server's DATABASE_URL and STORAGE_* exported; the global setup
## refuses a DATABASE_URL on TEST_DATABASE_URL's instance (Go tests churn it)
## and checks the seeders reach the server's database and storage.
e2e:
	cd web && pnpm test:e2e

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

## kind-smoke: run kind-based Helm chart smoke tests
kind-smoke:
	./scripts/kind-smoke.sh

clean:
	rm -rf bin web/out web/.next
	find $(WEB_DIST) -mindepth 1 ! -name .gitkeep -delete
