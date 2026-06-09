# AWS Migration Plan — Backend

**Status**: Draft for review
**Last updated**: 2026-06-08
**Reviewers**: jordi
**Phase**: 1 of 4 (this document covers only Phase 1 in detail)

---

## 1. Context

The backend currently runs as a long-lived Gin HTTP server on Railway with a PostgreSQL event store. We're migrating to AWS:

- **Event store**: PostgreSQL → DynamoDB
- **Runtime**: Long-lived HTTP server → Lambda behind API Gateway HTTP API
- **Read models** (projections): synchronous in-process → asynchronous via Kinesis-fed projector Lambda
- **Reactors / scheduled jobs**: in-process pollers → EventBridge-scheduled Lambdas / Kinesis consumers

The infra is already in place. After Phase 1 lands, `POST /wallets` etc. will work end-to-end on Lambda + DynamoDB. `GET` endpoints will return empty data until Phase 2 (projector) ships.

## 2. Goal of Phase 1

End-state after Phase 1 merges and deploys:

- Same Go codebase, same vertical slices, same handler / command-handler / aggregate structure.
- The store under every command handler is DynamoDB (via the existing `eventstore.Store` interface).
- The same Gin engine boots as either an HTTP server (`cmd/server`, for local dev) **or** as a Lambda (`cmd/lambda`, for deployment).
- POST endpoints write to DynamoDB with the same OCC guarantees as today.
- The transfer-money flow (`AppendMulti` across 3 aggregates) preserves atomicity via `TransactWriteItems`.

What's deliberately deferred:

- GET endpoints reading projection tables → stale/empty until Phase 2.
- Outbox-driven reactors (debt settlement, recurring execution) → stop running. Replaced in Phase 3.
- Recurring-movement processor → stops running. Replaced in Phase 3 by EventBridge schedule.
- Cognito auth → Phase 4. JWT stays custom for now.

---

## 3. Architectural shifts

### 3.1 Event store: Postgres → DynamoDB

The `Store` interface at `internal/shared/eventstore/store.go` already abstracts the store. A new `dynamodb.go` implementation slots in alongside the existing `memory.go` and `postgres.go`.

| `Store` method | DynamoDB mapping | Notes |
|---|---|---|
| `Append(aggID, events, expectedVersion)` | `PutItem` × 1 (single event), or `TransactWriteItems` (N events) | Each item carries `aggregate_id` (PK), `version = expectedVersion+i` (SK). Condition: `attribute_not_exists(version)`. |
| `AppendMulti(batches)` | `TransactWriteItems` (one Put per item, condition per aggregate) | Up to 100 items / 4 MB total — well above transfer-money's ~3 items. |
| `Load(aggID)` | `Query` with `PK = aggregate_id`, strongly consistent | Returns events ordered by version (SK). |
| `LoadAll()` | `Scan` (paginated) | Used only by `ReplayAll`. Acceptable for low volume; will be replaced by Kinesis replay in Phase 2. |
| `Subscribe(sub)` | **No-op for the DDB impl** | Projections move to the projector Lambda. The in-memory impl keeps the Subscriber behaviour for tests. |
| `AppendWithOutbox(...)` | Falls through to `Append` — outbox arg ignored | Outbox semantics replaced by DynamoDB → Kinesis fan-out. |
| `RegisterEvent(type, factory)` | Unchanged (Go map, in-process) | |
| `ReplayAll(ctx)` | `Scan` + dispatch | Not called in the Lambda entry point. |

Error mapping:

- `ConditionalCheckFailedException` → `ErrConcurrencyConflict`.
- `TransactionCanceledException` with any reason whose code is `ConditionalCheckFailed` → `ErrConcurrencyConflict`.
- All other AWS errors → wrapped via `fmt.Errorf("dynamodb: %w", err)`.

### 3.2 Runtime: HTTP server → Lambda

Use `github.com/awslabs/aws-lambda-go-api-proxy/gin` (the `NewV2` constructor for API Gateway HTTP API payload v2). The same `*gin.Engine` works for both:

- `cmd/server/main.go`: starts engine on `:PORT` via `engine.Run()`.
- `cmd/lambda/main.go`: wraps engine in `ginadapter.NewV2(engine)` and calls `lambda.Start(handler)`.

Cold start budget for a Go arm64 Lambda with the AWS SDK pre-initialised in `init()` is typically 150–300 ms; warm invocations <10 ms. Acceptable for a toy project; revisit with provisioned concurrency only if real latency complaints surface.

### 3.3 Projections (Phase 2 — outlined here only because of the impact)

Today: every `Append` calls `notify(ctx, events)` which invokes every subscriber synchronously. Subscribers write to Postgres read-model tables.

Future: the DDB store does **not** call `notify`. Read-model writes happen in the projector Lambda, which consumes the Kinesis stream. Until Phase 2 ships:

- GETs that read `wallets_view`, `movements`, `transactions_view`, `debts`, `recurring_movements_view` return empty data.
- The projection code in `internal/payments/.../projections/` stays in the repo; we will reuse it in Phase 2 inside the projector Lambda.

### 3.4 Background goroutines

Two long-lived loops exist today:

- `internal/shared/outbox/poller.go` — 2 s tick.
- `internal/payments/recurringmovement/.../processor.go` — 10 s tick.

Lambda is request-scoped — neither survives between invocations. For Phase 1: **do not start them in `cmd/lambda/main.go`**. Local `cmd/server/main.go` keeps starting them so the legacy loop still works for local Postgres-mode dev.

Phase 3 replaces them with:

- Outbox poller → DynamoDB Streams → Kinesis → reactor Lambdas (per reactor), no central poller.
- Recurring processor → EventBridge cron schedule → one-shot Lambda invocation.

---

## 4. Concrete file changes

### 4.1 Add

| Path | Purpose |
|---|---|
| `internal/shared/eventstore/dynamodb.go` | DynamoDB implementation of `Store`. |
| `internal/shared/eventstore/dynamodb_test.go` | Contract tests against LocalStack (or skipped if no DDB endpoint). |
| `internal/app/engine.go` | `BuildEngine(ctx, opts) (*gin.Engine, error)` — extracted from `cmd/server/main.go`. |
| `internal/app/store.go` | `BuildStore(ctx, cfg) (eventstore.Store, error)` — selects impl from `STORE` env var. |
| `internal/app/options.go` | `Options` struct: `StartBackgroundJobs bool`, etc. |
| `cmd/lambda/main.go` | Lambda entry point. Builds engine with `StartBackgroundJobs: false`, wraps in `ginadapter.NewV2`. |
| `Makefile` targets: `build-lambda`, `localstack-up`, `dev-dynamodb` | DX. |
| `.github/workflows/deploy.yml` | Build for `linux/arm64`, zip, `aws lambda update-function-code`. |

### 4.2 Modify

| Path | Why |
|---|---|
| `go.mod` / `go.sum` | Add `github.com/aws/aws-sdk-go-v2/{config,service/dynamodb}`, `github.com/awslabs/aws-lambda-go-api-proxy`, `github.com/aws/aws-lambda-go`. |
| `cmd/server/main.go` | Replace inline engine construction with `app.BuildEngine(ctx, app.Options{StartBackgroundJobs: true})`. Behaviour unchanged. |
| `internal/shared/eventstore/store.go` | Document that `Subscribe` is a no-op for DDB impl. No interface change. |
| `docker-compose.yml` | Add `localstack` service. |
| `README.md` (if any) | Update local-dev instructions for the new STORE modes. |

### 4.3 Leave alone (Phase 1 ends here)

- `internal/shared/eventstore/postgres.go` — kept for local Postgres-mode dev and as a reference.
- `internal/shared/database/migrations/` — kept; the projections still target these tables in Phase 2.
- `internal/shared/outbox/` — kept; the reactors will move to Kinesis consumers in Phase 3.
- All `projections/` subpackages — kept; reused in the projector Lambda in Phase 2.
- `internal/payments/recurringmovement/.../processor.go` — kept; will be invoked from a scheduled Lambda in Phase 3.

---

## 5. DynamoDB store — implementation details

### 5.1 Item shape

Mirrors the schema documented in the Terraform `eventstore` module:

| Attribute | Type | Notes |
|---|---|---|
| `aggregate_id` | S (PK) | e.g. `wallet#01HXYZ...`. The slice prefix mirrors how aggregate IDs are formatted in code today (UUIDs) — keep the existing format, just store as-is. |
| `version` | N (SK) | Starts at 1, monotonic per aggregate. |
| `event_id` | S | Use UUIDv7 (`github.com/google/uuid` v1.6+ supports it). Time-ordered, plays well with stream ordering. |
| `event_type` | S | e.g. `WalletAdded`. Matches the existing `Event.Type()` value. |
| `event_payload` | M | Marshalled domain event. Use `attributevalue.MarshalMap`. Avoid `json.Marshal` + dumping a string — keeping it structured lets future tooling read it natively. |
| `correlation_id` | S | Propagated from gin context. |
| `causation_id` | S | Event ID of the cause; empty string for command-originated events. |
| `occurred_at` | S | ISO-8601, when the domain decision was made. |
| `recorded_at` | S | ISO-8601, AWS-side `time.Now().UTC()`. |

### 5.2 Constructor

```go
type DynamoStore struct {
    client    *dynamodb.Client
    tableName string
    factories map[string]func() Event  // existing RegisterEvent map
}

func NewDynamoStore(ctx context.Context, tableName string) (*DynamoStore, error) {
    cfg, err := config.LoadDefaultConfig(ctx)
    if err != nil {
        return nil, fmt.Errorf("load aws config: %w", err)
    }
    return &DynamoStore{
        client:    dynamodb.NewFromConfig(cfg),
        tableName: tableName,
        factories: make(map[string]func() Event),
    }, nil
}
```

For LocalStack: caller sets `AWS_ENDPOINT_URL=http://localhost:4566` env var, which SDK v2 reads automatically (≥ Go SDK v1.23). No custom resolver code needed.

### 5.3 Append — single event

```go
func (s *DynamoStore) Append(ctx context.Context, aggID string, events []Event, expectedVersion int) error {
    if len(events) == 0 {
        return nil
    }
    if len(events) == 1 {
        item := s.itemFor(aggID, expectedVersion+1, events[0], extractMeta(ctx))
        _, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
            TableName:           aws.String(s.tableName),
            Item:                item,
            ConditionExpression: aws.String("attribute_not_exists(version)"),
        })
        return mapErr(err)
    }
    // ≥ 2 events: TransactWriteItems
    return s.appendBatch(ctx, aggID, events, expectedVersion)
}
```

### 5.4 Append — multiple events for the same aggregate (TransactWriteItems)

`TransactWriteItems` with one `Put` per event, condition `attribute_not_exists(version)` on each. Up to 100 items per transaction; we'd need a command emitting more than 100 events from a single decision to hit it.

### 5.5 AppendMulti — multiple aggregates atomic

Used by `transfermoney` to update 3 aggregates atomically. Single `TransactWriteItems` call with one `Put` per (aggregate, version), condition `attribute_not_exists(version)` per Put. Maps cleanly. If any condition fails → entire transaction aborts → `ErrConcurrencyConflict` returned.

### 5.6 Load

`Query` with:

- `KeyConditionExpression`: `aggregate_id = :id`
- `ConsistentRead`: `true` (we always want the latest state for command handling)
- `ScanIndexForward`: `true` (ascending by version)

Iterate pages until done. Materialise into `[]Event` by looking up the factory in `s.factories[event_type]`.

### 5.7 LoadAll / ReplayAll

`Scan` with pagination. Not used in the Lambda entry point. Kept functional for the in-memory and Postgres impls; for DDB it'd be slow but acceptable for offline replay.

### 5.8 Subscribe / notify

```go
func (s *DynamoStore) Subscribe(_ Subscriber)        {} // no-op
func (s *DynamoStore) notify(_ context.Context, _ []Event) {} // not exposed
```

A `slog.Warn` on the first Subscribe call could be useful to detect misuse but is optional.

### 5.9 AppendWithOutbox

The outbox parameter is silently dropped. A short comment explains that Kinesis fan-out replaces this in Phase 3.

### 5.10 Error mapping helper

```go
func mapErr(err error) error {
    if err == nil { return nil }
    var ccfe *types.ConditionalCheckFailedException
    if errors.As(err, &ccfe) {
        return ErrConcurrencyConflict
    }
    var tce *types.TransactionCanceledException
    if errors.As(err, &tce) {
        for _, r := range tce.CancellationReasons {
            if aws.ToString(r.Code) == "ConditionalCheckFailed" {
                return ErrConcurrencyConflict
            }
        }
    }
    return fmt.Errorf("dynamodb: %w", err)
}
```

---

## 6. Lambda entry point — implementation details

### 6.1 Structure

```go
// cmd/lambda/main.go
package main

import (
    "context"
    "log"
    "log/slog"
    "os"

    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-lambda-go/lambda"
    ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"

    "money-management/backend/internal/app"
)

var ginLambda *ginadapter.GinLambdaV2

func init() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    engine, err := app.BuildEngine(context.Background(), logger, app.Options{
        StartBackgroundJobs: false,
    })
    if err != nil {
        log.Fatalf("build engine: %v", err)
    }
    ginLambda = ginadapter.NewV2(engine)
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
    return ginLambda.ProxyWithContext(ctx, req)
}

func main() { lambda.Start(handler) }
```

### 6.2 Why `init()` for engine construction

- One-time per cold start, amortised across warm invocations.
- AWS SDK v2 clients are cheap to create — no network calls in `LoadDefaultConfig` or `dynamodb.NewFromConfig`.
- If construction fails (e.g. `STORE=dynamodb` but no IAM perms), the function panics on init — every invocation 500s until redeployed. That's the right behaviour; better than silently degrading.

### 6.3 What the handler does

`ginadapter.ProxyWithContext` translates API GW v2 events into `http.Request`, calls the gin engine via `ServeHTTP`, captures the response, translates back. The gin engine sees a normal request and runs all its middleware (CORS, correlation ID, logging, JWT auth) and slice handlers untouched.

---

## 7. Configuration

### 7.1 New env var: `STORE`

| Value | Behaviour |
|---|---|
| `dynamodb` (default in Lambda) | Constructs `DynamoStore` using `AWS_REGION` + `EVENTS_TABLE`. |
| `postgres` | Existing Postgres impl. Used by local `cmd/server` if you want the legacy loop. |
| `memory` | In-memory store. For quick local iteration without infra. |

`cmd/lambda/main.go` defaults `STORE` to `dynamodb` if unset.
`cmd/server/main.go` defaults `STORE` to `postgres` if unset.

### 7.2 New env var: `EVENTS_TABLE`

DynamoDB table name. In dev it's `mm-events-dev`, set on the Lambda function by Terraform (already wired in `envs/dev/main.tf` → `module.payments_lambda.environment`).

### 7.3 `AWS_REGION`

Provided automatically by the Lambda runtime. Locally with LocalStack: set in `.envrc` or `docker-compose.yml`.

### 7.4 `AWS_ENDPOINT_URL` (LocalStack only)

SDK v2 reads this automatically. Set in `docker-compose.yml` for the local `app` service.

### 7.5 `JWT_SECRET`

Kept as a plain Lambda environment variable for Phase 1. Marked as a Phase 4 improvement to move to Secrets Manager / SSM Parameter Store. The secret itself needs to be set in Terraform (`module.payments_lambda.environment`) — coordinate with infra PR.

### 7.6 Removed env vars in the Lambda entry point

- `DATABASE_URL` — not read by the DDB store. Still used by `cmd/server` when `STORE=postgres`.
- `PORT` — not read by Lambda runtime.

---

## 8. Wiring refactor — `internal/app/`

Today `cmd/server/main.go` is ~200 lines of inline wiring. To avoid duplicating it in `cmd/lambda/main.go`, extract:

```
internal/app/
├── engine.go    # BuildEngine(ctx, logger, opts) (*gin.Engine, error)
├── store.go     # BuildStore(ctx, logger, opts) (eventstore.Store, func() error, error)
│                #   ^ returns a cleanup func so cmd/server can defer it
├── routes.go    # registerRoutes(engine, store, deps...) — calls every slice's Register*
└── options.go   # Options struct
```

The `cmd/server/main.go` becomes ~50 lines: build engine with `StartBackgroundJobs: true`, `engine.Run(":" + os.Getenv("PORT"))`.

This is a pure refactor — no behaviour change. Should be its own commit within the PR for review clarity.

---

## 9. Testing strategy

### 9.1 Untouched

- Aggregate G/W/T tests (`*_aggregate_test.go`) — pure domain, no store.
- Handler tests using in-memory store — these continue to validate the slices.

### 9.2 New: DDB store contract tests

`internal/shared/eventstore/dynamodb_test.go` covering:

| Case | Expected |
|---|---|
| Append v1 (expectedVersion=0) on empty aggregate | OK |
| Append v1 again | `ErrConcurrencyConflict` |
| Append v2 after v1 (expectedVersion=1) | OK |
| Append v3 with expectedVersion=1 (skipping) | `ErrConcurrencyConflict` |
| Append 5 events in one call (expectedVersion=2) | All 5 land at v3..v7 |
| AppendMulti across 3 aggregates, all clean | All 3 versions advance |
| AppendMulti where 1 of 3 has stale expectedVersion | Entire batch rolled back, `ErrConcurrencyConflict` |
| Load returns events in version order | |
| Load on empty aggregate | `[]Event{}, nil` |

Tests skip themselves if `AWS_ENDPOINT_URL` isn't set, so they don't fail in pure-unit CI. CI step runs LocalStack as a service container and sets `AWS_ENDPOINT_URL=http://localstack:4566`.

### 9.3 New: handler-tests-with-DDB-store (optional, can defer)

Re-run a couple of existing handler tests with the DDB store wired in. Sanity check that real persistence works with the same observable behaviour. Could be skipped if the contract tests + a manual smoke test on the deployed Lambda are deemed sufficient.

### 9.4 Manual smoke test (after deploy)

```sh
API_URL=$(aws apigatewayv2 get-apis --query "Items[?Name=='mm-api-dev'].ApiEndpoint | [0]" --output text)
curl -i -X POST "$API_URL/wallets" \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <dev JWT>' \
  -d '{"name":"smoke-test","currency":"EUR"}'
# Expect: 201, with a wallet ID
aws dynamodb query \
  --table-name mm-events-dev \
  --key-condition-expression 'aggregate_id = :id' \
  --expression-attribute-values '{":id":{"S":"wallet#<id from above>"}}'
# Expect: 1 item with version=1, event_type=WalletAdded
```

---

## 10. CI / Deploy pipeline

### 10.1 New workflow: `.github/workflows/deploy.yml` in the backend repo

```yaml
name: Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

permissions:
  id-token: write
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: go test ./...

  deploy:
    needs: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - name: build linux/arm64 binary
        run: |
          GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
            go build -tags lambda.norpc -trimpath -ldflags='-s -w' \
            -o bootstrap ./cmd/lambda
          zip function.zip bootstrap
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ vars.AWS_DEPLOY_ROLE_ARN }}
          aws-region: eu-west-1
      - run: |
          aws lambda update-function-code \
            --function-name mm-payments-dev \
            --zip-file fileb://function.zip
          aws lambda wait function-updated \
            --function-name mm-payments-dev
```

### 10.2 Repo variables to set on `money-management-backend`

| Variable | Value | Source |
|---|---|---|
| `AWS_DEPLOY_ROLE_ARN` | `arn:aws:iam::427637994672:role/mm-backend-deploy-dev` | `terraform output backend_deploy_role_arn` from `money-management-infra/envs/dev` |
| `AWS_REGION` | `eu-west-1` | Same as infra repo |

### 10.3 Open question — split `test` from `deploy`?

Above splits them. Pros: failed tests don't deploy. Cons: adds ~30 s to the pipeline (two checkouts, two Go installs). Recommendation: keep split.

---

## 11. Execution order within the PR

Designed so that each step is independently reviewable and the working tree compiles + tests pass at every checkpoint.

1. **Pure refactor** — extract `internal/app/{engine,store,routes,options}.go` from `cmd/server/main.go`. No behaviour change. Tests still green. *Commit 1.*
2. **Add DDB store implementation** + contract tests (skipped without LocalStack). `STORE=dynamodb` not yet wired into the selector. *Commit 2.*
3. **Wire selector** — `app.BuildStore` reads `STORE` env var. `cmd/server` default unchanged (`postgres`). *Commit 3.*
4. **Add `cmd/lambda/main.go`** + go.mod additions for `aws-lambda-go`, `aws-lambda-go-api-proxy`. *Commit 4.*
5. **Make `cmd/lambda` skip background jobs** by passing `StartBackgroundJobs: false`. *(Could fold into commit 4.)*
6. **Add `docker-compose.yml` LocalStack service** + Makefile targets. *Commit 5.*
7. **Add deploy workflow + README updates**. *Commit 6.*

Total: ~6 small commits in one PR. ~600 LOC added, ~150 LOC moved.

---

## 12. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| DDB OCC behaves subtly differently than Postgres `MAX(version)` check | Med | Med | Contract tests cover all OCC edges. Pair with a manual smoke test that double-submits the same command. |
| `TransactWriteItems` error decoding is fiddly | Med | Low | Helper `mapErr` is unit-tested in isolation. |
| Lambda env var size limit (4 KB total) | Low | Low | Currently: `STORE`, `EVENTS_TABLE`, `JWT_SECRET`, ~5 more. Far below. |
| Cold start latency on first hit after deploy | Med | Low | ~250 ms typical for Go arm64. Acceptable for toy project. Provisioned concurrency is a $-cost lever we don't need. |
| LocalStack DDB diverges from real DDB | Low | Med | Run an integration test once in real AWS dev after first deploy. After that, LocalStack is the inner loop. |
| GETs return empty data confuses anyone testing the frontend | High | Low | Document loudly. Frontend isn't wired anyway in Phase 1. |
| Outbox poller being off breaks debt settlement in dev | High | Low | Toy project, not in production. Phase 3 restores. |
| The Subscriber pattern is partly used for ID generation in projections (deterministic IDs from `meta.EventID`) | Low | Low | Phase 2 projector will pass `meta.EventID` from Kinesis records. No interface change needed today. |
| `cmd/server` Postgres mode + DDB mode coexisting causes drift | Med | Low | Encourage `STORE=memory` for quick iteration; reserve `STORE=postgres` for "I want to test the legacy stack." Deprecate Postgres mode after Phase 2 lands. |

---

## 13. Out of scope (explicit)

These are NOT in Phase 1, by design:

- Cognito + API GW JWT authorizer (Phase 4).
- Aurora Serverless v2 + projector Lambda + read-model writes (Phase 2).
- Reactor Lambdas replacing the outbox poller (Phase 3).
- EventBridge cron for recurring movements (Phase 3).
- Deletion of `postgres.go`, `migrations/`, projection packages (no earlier than Phase 2, possibly later).
- CORS configuration on API GW (Phase 4 with the frontend).
- X-Ray trace propagation through `correlation_id` (improvement).
- VPC for Aurora reachability (Phase 2).
- SSM / Secrets Manager for `JWT_SECRET` (Phase 4 hardening).
- Frontend deployment (CloudFront + S3) (Phase 4).
- Observability dashboards beyond the default log groups (Phase 4 or 5).

---

## 14. Future phases — sketched, not committed

### Phase 2: Projector + Aurora Serverless v2

- Terraform: VPC, 2 AZs, single NAT, Aurora Serverless v2 cluster (0 ACU min), DB subnet group, security groups, secrets in Secrets Manager.
- Terraform: new `modules/projector` consuming the Kinesis stream from `module.eventstore`, attached to VPC.
- Backend: extract projection packages into a new `cmd/projector` that mirrors the per-slice projector pattern but is invoked per-record from Kinesis events.
- Backend: migrations runner becomes a separate one-shot Lambda invoked from CI (or `null_resource` in Terraform).
- GETs come back to life.

### Phase 3: Reactors + scheduled jobs

- Reactor Lambdas: one per concerned slice (debt settlement, etc.), consume the Kinesis stream, replace the outbox-poller machinery. Outbox poller and outbox table are removed.
- Recurring movement processor: EventBridge cron → one-shot Lambda → emits commands.

### Phase 4: Cognito + frontend + hardening

- Cognito user pool + app client + hosted UI (or Amplify Auth on the web side).
- API GW JWT authorizer using the Cognito pool. Drop custom JWT.
- CloudFront + S3 + ACM (us-east-1) for the frontend.
- CORS on API GW.
- Secrets Manager for any remaining secrets.

### Phase 5 (maybe): observability uplift

- CloudWatch dashboards per slice.
- Alarms on Lambda errors / API GW 5xx / Kinesis IteratorAge.
- X-Ray service map.
- Cost anomaly detection.

---

## 15. Open questions for review

These are decisions I want a yes/no on before starting Phase 1:

1. **Scope confirmation** — Phase 1 as defined (DDB store + Lambda entry point, GETs broken, background jobs off). Y/N?
2. **`STORE=postgres` mode remains for local dev** during Phase 1. Y/N? If N, delete `postgres.go` and migrations in this PR too.
3. **`AppendMulti` via `TransactWriteItems`** — preserve atomic transfers in Phase 1. Y/N?
4. **`memory` store keeps using `Subscribe`/`notify`** so in-process tests still exercise projection wiring. Y/N? If N, drop the Subscribe surface entirely from the interface.
5. **Repo layout for the wiring extraction** — `internal/app/`. Y/N? Alternative: leave wiring in `cmd/server/main.go` and have `cmd/lambda/main.go` import it. (I prefer `internal/app/`.)
6. **Deploy on push to main** vs **deploy only on tag**. I propose push-to-main for the toy-project velocity. Y/N?
7. **Skip integration tests on PR runs, only run on main** to keep PR feedback fast? Or always run them? I propose always run.
8. **`AWS_DEPLOY_ROLE_ARN` lives where in the backend repo** — Variables (visible in logs, easy debug) vs Secrets (hidden). It's a non-secret ARN, so Variables. Y/N?

---

## 16. Glossary

- **OCC** — Optimistic Concurrency Control. Reads current version, appends with a condition that version hasn't moved.
- **Append / AppendMulti** — single- vs. multi-aggregate atomic event-store writes.
- **Subscribe / notify** — in-process pub/sub used by today's sync projections. Going away on the DDB path.
- **`TransactWriteItems`** — DynamoDB API for atomic writes across up to 100 items in one region.
- **Cold start** — initial invocation of a Lambda container, includes language runtime + your `init()` + handler call.
- **Provisioned concurrency** — paid pre-warmed Lambda instances. Not used here.

---

**End of plan. Mark up directly with comments, or send replies via questions in §15.**
