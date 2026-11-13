# agent-dash developer tasks. Tool versions come from mise.toml (`mise install`);
# dev environment variables come from mise.toml's [env].

WEB_DIST := internal/webui/dist
IMAGE    ?= agent-dash:dev

.PHONY: all web build run db-up db-down test vet lint web-lint web-typecheck e2e docker clean

all: web build

## web: build the Next.js export and copy it where internal/webui embeds it
web:
	cd web && pnpm install --frozen-lockfile && pnpm build
	find $(WEB_DIST) -mindepth 1 ! -name .gitkeep -delete
	cp -R web/out/. $(WEB_DIST)/

## build: compile the server (embeds whatever is in internal/webui/dist)
build:
	CGO_ENABLED=0 go build -trimpath -o bin/agent-dash ./cmd/agent-dash

## run: build everything and start the server against the Compose database
run: web build db-up
	./bin/agent-dash

db-up:
	docker compose up -d --wait postgres

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

## e2e: routing acceptance check against a running server (BASE_URL)
e2e:
	cd web && pnpm test:e2e

docker:
	docker build -t $(IMAGE) .

clean:
	rm -rf bin web/out web/.next
	find $(WEB_DIST) -mindepth 1 ! -name .gitkeep -delete
