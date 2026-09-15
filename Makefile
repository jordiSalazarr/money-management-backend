.PHONY: help test test-ddb build run build-lambda build-projector build-migrate build-reactor build-recurring-cron build-all-lambdas localstack-up localstack-down dev-dynamodb dev-stack postgres-up postgres-down dev-migrate dev-reset dev-seed create-local-tables

LOCALSTACK_ENDPOINT ?= http://localhost:4566
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

# NOTE: dev-stack is parked until phase 2.
#
# It ran cmd/server against DynamoDB with the in-process async pipeline. That
# pipeline is now a real subscription to the store's global log, and DynamoDB
# has no global log — its read side was a Kinesis export. So the projector, the
# process reactors, the cron and the sweeper cannot start against it, and the
# stack would serve empty read models with no transfer ever completing.
#
# `make run` is the full local experience in the meantime: in-memory store with
# every one of those pieces running. dev-stack comes back in phase 2 pointed at
# KurrentDB, when there is a real log to subscribe to.
dev-stack: ## (parked until phase 2 — use `make run`) DDB + Aurora + async pipeline
	@echo "dev-stack is parked until the KurrentDB adapter lands in phase 2."
	@echo "DynamoDB has no global log, so the projector and reactors cannot subscribe to it."
	@echo ""
	@echo "Use 'make run' instead: in-memory store with the projector, the three"
	@echo "process reactors, the recurring cron and the sweeper all running in process."
	@exit 1

dev-reset: localstack-up postgres-up ## Wipe DDB events + projection tables, then re-run migrations
	EVENTS_TABLE=$(EVENTS_TABLE) AURORA_URL=$(AURORA_URL) $(LS_AWS_ENV) \
		go run ./cmd/devtools reset

dev-seed: ## Populate a running dev-stack with a small set of wallets + movements
	go run ./cmd/devtools seed
