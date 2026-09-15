.PHONY: help test test-kurrent replay kurrentdb-up kurrentdb-down dev-kurrent dev-stack build run postgres-up postgres-down dev-migrate dev-reset dev-nuke dev-seed

KURRENTDB_URL       ?= kurrentdb://localhost:2113?tls=false
KURRENTDB_HTTP      ?= http://localhost:2113
AURORA_URL          ?= postgres://money:money@localhost:5432/money_management?sslmode=disable

# Static creds keep the AWS CLI happy when talking to LocalStack — it
# rejects calls with no creds at all even though it doesn't validate them.

help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' Makefile | awk 'BEGIN{FS=":.*?## "}{printf "  %-22s %s\n", $$1, $$2}'

test: ## Run all Go tests (DDB contract tests skip without AWS_ENDPOINT_URL)
	go test ./...

build: ## Native build of every entry point
	go build ./...

run: ## Start cmd/server with the in-memory store + full in-process pipeline (no infra needed)
	go run ./cmd/server

postgres-up: ## Start the Postgres container (idempotent)
	docker compose up -d postgres
	@until docker compose exec -T postgres pg_isready -U money -d money_management >/dev/null 2>&1; do \
		echo "waiting for postgres..."; sleep 1; \
	done
	@echo "postgres ready at localhost:5432"

postgres-down: ## Stop the Postgres container
	docker compose stop postgres

dev-migrate: postgres-up ## Apply any unapplied Postgres migrations (idempotent)
	AURORA_URL=$(AURORA_URL) go run ./cmd/devtools migrate

kurrentdb-up: ## Start the KurrentDB container (idempotent)
	docker compose up -d kurrentdb
	@until curl -fsS $(KURRENTDB_HTTP)/health/live >/dev/null 2>&1; do \
		echo "waiting for kurrentdb..."; sleep 1; \
	done
	@echo "kurrentdb ready at $(KURRENTDB_HTTP) (ui: $(KURRENTDB_HTTP))"

kurrentdb-down: ## Stop the KurrentDB container
	docker compose stop kurrentdb

test-kurrent: kurrentdb-up ## Run the contract + saga tests against a real KurrentDB (stop dev-stack first)
	@if pgrep -f "/server$$" >/dev/null 2>&1; then \
		echo "A server is running and is subscribed to \$$all — it will drive the same"; \
		echo "transfers these tests drive, and they will collide. Stop it first."; \
		exit 1; \
	fi
	KURRENTDB_URL=$(KURRENTDB_URL) go test ./internal/shared/eventstore/ -run TestKurrentStore -v
	KURRENTDB_URL=$(KURRENTDB_URL) go test ./internal/payments/processes/... -run Kurrent -v

dev-stack: kurrentdb-up postgres-up dev-migrate ## Run cmd/server against KurrentDB + Postgres with the async pipeline
	STORE=kurrentdb \
	KURRENTDB_URL=$(KURRENTDB_URL) \
	AURORA_URL=$(AURORA_URL) \
		go run ./cmd/server

dev-kurrent: kurrentdb-up ## Run cmd/server against KurrentDB with in-memory read models (no Postgres)
	STORE=kurrentdb KURRENTDB_URL=$(KURRENTDB_URL) go run ./cmd/server

dev-reset: postgres-up ## Wipe the projection tables, then re-run migrations
	AURORA_URL=$(AURORA_URL) go run ./cmd/devtools reset

# A full reset has to take the event store with it.
#
# Wiping only the projections leaves them empty with their checkpoints intact,
# so nothing replays and the read models stay blank — worse than not resetting
# at all. KurrentDB has no "delete everything", so the volume goes.
replay: ## Rebuild ONE projection (make replay P=movements_projector)
	@test -n "$(P)" || { echo "usage: make replay P=<projection>"; AURORA_URL=$(AURORA_URL) go run ./cmd/devtools replay 2>&1 | tail -8; exit 2; }
	AURORA_URL=$(AURORA_URL) go run ./cmd/devtools replay $(P)

dev-nuke: ## Wipe BOTH the event store and the projections, then re-migrate
	docker compose rm -sf kurrentdb
	docker volume rm -f $$(docker compose config --volumes | grep kurrent | sed "s|^|$$(basename $(CURDIR))_|") 2>/dev/null || true
	$(MAKE) kurrentdb-up
	docker compose exec -T postgres psql -U money -d money_management -c "DROP TABLE IF EXISTS projection_checkpoints;" >/dev/null 2>&1 || true
	$(MAKE) dev-reset
	@echo "event store and projections are empty; checkpoints cleared"

dev-seed: ## Populate a running dev-stack with a small set of wallets + movements
	go run ./cmd/devtools seed
