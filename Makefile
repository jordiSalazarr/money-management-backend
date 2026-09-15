.PHONY: help test test-ddb test-kurrent kurrentdb-up kurrentdb-down dev-kurrent build run build-lambda build-projector build-migrate build-reactor build-recurring-cron build-all-lambdas localstack-up localstack-down dev-dynamodb dev-stack postgres-up postgres-down dev-migrate dev-reset dev-nuke dev-seed create-local-tables

LOCALSTACK_ENDPOINT ?= http://localhost:4566
KURRENTDB_URL       ?= kurrentdb://localhost:2113?tls=false
KURRENTDB_HTTP      ?= http://localhost:2113
AWS_REGION          ?= eu-west-1
EVENTS_TABLE        ?= mm-events-dev
USERS_TABLE         ?= mm-users-dev
AURORA_URL          ?= postgres://money:money@localhost:5432/money_management?sslmode=disable

# Static creds keep the AWS CLI happy when talking to LocalStack — it
# rejects calls with no creds at all even though it doesn't validate them.
LS_AWS_ENV := AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=$(AWS_REGION) AWS_ENDPOINT_URL=$(LOCALSTACK_ENDPOINT)

help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' Makefile | awk 'BEGIN{FS=":.*?## "}{printf "  %-22s %s\n", $$1, $$2}'

test: ## Run all Go tests (DDB contract tests skip without AWS_ENDPOINT_URL)
	go test ./...

test-ddb: localstack-up create-local-tables ## Run DDB contract tests against LocalStack
	$(LS_AWS_ENV) go test ./internal/shared/eventstore/ -run TestDynamo -v

build: ## Native build of every entry point
	go build ./...

run: ## Start cmd/server with the in-memory store + full in-process pipeline (no infra needed)
	go run ./cmd/server

# Generic Lambda zip target. Call with CMD=lambda|projector|migrate|reactor|recurring-cron.
# Each invocation produces <CMD>.zip containing a bootstrap binary.
build-lambda: ## Cross-compile a cmd/$(CMD) Lambda zip (CMD=lambda|projector|migrate|reactor|recurring-cron)
	@test -n "$(CMD)" || { echo "set CMD=lambda|projector|migrate|reactor|recurring-cron"; exit 1; }
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -tags lambda.norpc -trimpath -ldflags='-s -w' \
		-o bootstrap ./cmd/$(CMD)
	zip -j $(CMD).zip bootstrap
	rm bootstrap
	@ls -lh $(CMD).zip

build-projector: ## Convenience target — build cmd/projector zip
	$(MAKE) build-lambda CMD=projector

build-migrate: ## Convenience target — build cmd/migrate zip
	$(MAKE) build-lambda CMD=migrate

build-reactor: ## Convenience target — build cmd/reactor zip
	$(MAKE) build-lambda CMD=reactor

build-recurring-cron: ## Convenience target — build cmd/recurring-cron zip
	$(MAKE) build-lambda CMD=recurring-cron

build-all-lambdas: ## Build all five Lambda zips in sequence
	$(MAKE) build-lambda CMD=lambda
	$(MAKE) build-lambda CMD=projector
	$(MAKE) build-lambda CMD=migrate
	$(MAKE) build-lambda CMD=reactor
	$(MAKE) build-lambda CMD=recurring-cron

localstack-up: ## Start the LocalStack DDB container (idempotent)
	docker compose up -d localstack
	@until curl -fsS $(LOCALSTACK_ENDPOINT)/_localstack/health >/dev/null 2>&1; do \
		echo "waiting for localstack..."; sleep 1; \
	done
	@echo "localstack ready at $(LOCALSTACK_ENDPOINT)"

localstack-down: ## Stop the LocalStack container
	docker compose stop localstack

create-local-tables: ## Create mm-events-dev + mm-users-dev tables in LocalStack
	@$(LS_AWS_ENV) aws dynamodb describe-table --table-name $(EVENTS_TABLE) >/dev/null 2>&1 || \
		$(LS_AWS_ENV) aws dynamodb create-table \
			--table-name $(EVENTS_TABLE) \
			--attribute-definitions AttributeName=aggregate_id,AttributeType=S AttributeName=version,AttributeType=N \
			--key-schema AttributeName=aggregate_id,KeyType=HASH AttributeName=version,KeyType=RANGE \
			--billing-mode PAY_PER_REQUEST >/dev/null
	@$(LS_AWS_ENV) aws dynamodb describe-table --table-name $(USERS_TABLE) >/dev/null 2>&1 || \
		$(LS_AWS_ENV) aws dynamodb create-table \
			--table-name $(USERS_TABLE) \
			--attribute-definitions AttributeName=email,AttributeType=S \
			--key-schema AttributeName=email,KeyType=HASH \
			--billing-mode PAY_PER_REQUEST >/dev/null
	@echo "tables ready: $(EVENTS_TABLE), $(USERS_TABLE)"

dev-dynamodb: localstack-up create-local-tables ## Run cmd/server against LocalStack DDB
	STORE=dynamodb EVENTS_TABLE=$(EVENTS_TABLE) USERS_TABLE=$(USERS_TABLE) $(LS_AWS_ENV) \
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
dev-nuke: ## Wipe BOTH the event store and the projections, then re-migrate
	docker compose rm -sf kurrentdb
	docker volume rm -f $$(docker compose config --volumes | grep kurrent | sed "s|^|$$(basename $(CURDIR))_|") 2>/dev/null || true
	$(MAKE) kurrentdb-up
	docker compose exec -T postgres psql -U money -d money_management -c "DROP TABLE IF EXISTS projection_checkpoints;" >/dev/null 2>&1 || true
	$(MAKE) dev-reset
	@echo "event store and projections are empty; checkpoints cleared"

dev-seed: ## Populate a running dev-stack with a small set of wallets + movements
	go run ./cmd/devtools seed
