# AWS Migration Plan — Backend, Phase 2

**Status**: Draft for review
**Last updated**: 2026-06-09
**Reviewers**: jordi
**Phase**: 2 of 4 (Projector Lambda + Aurora Serverless v2)

This is the detailed plan for Phase 2 of the broader migration laid out in [aws-migration-plan.md](./aws-migration-plan.md). Phase 1 (DDB event store + Lambda HTTP entry) landed on 2026-06-09 (commit `08e0045`). Phase 2's goal is to bring GET endpoints back to life.

---

## 1. Context

After Phase 1, the system writes events to DynamoDB through a Lambda behind API Gateway. GETs are mounted only in `cmd/server` (Postgres-mode local dev); in Lambda mode they aren't registered at all, so the deployed API responds 404 to any read.

The pieces that have to land for GETs to work in Lambda:

- **A read-model store** that lives outside the per-request Lambda lifetime. Aurora Serverless v2 (Postgres-compatible) is the destination — it scales to 0 ACU when idle and the existing migrations + `*PostgresStore` projection types port over directly.
- **A projector** that consumes the DynamoDB write stream and updates the read models. Phase 1 explicitly turned `Subscribe` into a no-op on the DDB store; the projector Lambda replaces that fan-out.
- **A VPC** to host Aurora and let the payments Lambda reach it.
- **A migrations runner** to keep Aurora's schema in lockstep with `internal/shared/database/migrations/`.

Nothing about Phase 1 needs to change for Phase 2 to slot in. `cmd/server` keeps working as the local Postgres harness.

## 2. Goal of Phase 2

End-state after Phase 2 merges and deploys:

- The payments Lambda services both POSTs (writes to DDB) and GETs (reads from Aurora).
- A separate projector Lambda consumes a Kinesis Data Stream fed by DynamoDB and updates Aurora read-model tables.
- The voice slice works on Lambda (it depends on projection stores; once those exist, it lights up).
- Schema migrations are owned by a dedicated runner that CI invokes on each deploy.
- Existing handler-tests-with-memory-store stay green; new integration tests cover the Aurora projection path.

What's deliberately deferred:

- **Outbox-driven reactors** (debt settlement, recurring execution) — they need stream consumption too, but the routing logic is different. Phase 3.
- **Recurring-movement processor** — Phase 3 (EventBridge cron).
- **Cognito** — Phase 4.

---

## 3. Architectural shifts

### 3.1 DynamoDB → Kinesis → projector Lambda

The DDB events table gets a [Kinesis Data Streams export](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/kds.html) attached to it. Every committed write lands as a `dynamodb` record on the stream with a fresh `eventID` and the full new image. The projector Lambda is triggered by Kinesis with a small batch size (start with 10) and per-shard concurrency 1 — read models must be applied in shard order to honour event ordering per aggregate.

Why Kinesis over plain DynamoDB Streams:

- Kinesis supports multiple consumers without invocation cost contention. Phase 3 reactors will plug into the same stream.
- Kinesis has configurable retention (24h default; up to 365d at extra cost). DDB Streams is fixed at 24h. Retention matters for re-running projections after a bug fix.
- Lambda integration with both is similar — Kinesis adds a per-second cost per shard but stays in the toy-project zone.

What it costs: ~$0.015/hr for a single-shard Kinesis stream + per-million-record charges. Negligible.

### 3.2 Read models: Postgres → Aurora Serverless v2

Aurora Serverless v2 in `eu-west-1`, Postgres engine, 0 ACU minimum and 2 ACU maximum. Provisioned ACUs cost ~$0.06/ACU-hour when active; at 0 ACU when idle the cluster costs almost nothing.

The existing `internal/shared/database/migrations/` files target Aurora unchanged — Aurora speaks Postgres. The events-store and outbox tables (`001`, `005`) become dead schema on Aurora (events live in DDB now; outbox lives in Kinesis later). I'll leave them in place rather than fork the migrations — phase 3 cleanup deletes them as part of the outbox decommission.

The existing `*PostgresStore` projection types (`movements_postgres_store.go`, `wallets_postgres_store.go`, etc.) work against Aurora as-is — `pgxpool` doesn't care that the other end is Aurora.

### 3.3 GET path: payments Lambda gains Aurora reads

`cmd/lambda` keeps the existing DDB-backed writes path. It also gets a `pgxpool` connection to Aurora and constructs the same projection stores `cmd/server` does today. `app.Options.MountQueryRoutes` flips back to `true` in Lambda mode.

This means the payments Lambda has to live in the VPC. Cold start in VPC is ~1–3 s for Go; warm invocations are unaffected. Acceptable for a toy project, and provisioned concurrency is a $-lever if real users complain.

The voice slice depends on projection stores — once they exist in Lambda mode, voice lights up automatically (gated on `ANTHROPIC_API_KEY`).

### 3.4 VPC + connectivity

A new VPC with:

- 2 AZs (`eu-west-1a`, `eu-west-1b`) for Aurora's HA requirement.
- Private subnets for Aurora and both Lambdas (payments + projector).
- Public subnets only as the home for the single NAT gateway.
- VPC endpoints (Gateway) for **DynamoDB** and **S3**.
- VPC endpoint (Interface) for **Kinesis Data Streams**, **Secrets Manager**, and **SSM Parameter Store**.

Why the endpoints: the payments Lambda needs to talk to DDB from inside the VPC. Without a Gateway endpoint, those calls round-trip through the NAT and incur NAT data charges. Interface endpoints have an hourly cost (~$0.01/hr each); Gateway endpoints are free. For a toy project the NAT charges dominate, so all DDB/Kinesis traffic should ride the endpoints.

Security groups:

- Aurora SG accepts inbound 5432 from the two Lambda SGs only.
- Lambda SGs allow all outbound (default).

### 3.5 Migrations runner

Schema changes land in `internal/shared/database/migrations/` like today. A small `cmd/migrate` binary runs the same `database.Migrate(databaseURL)` call cmd/server uses, then exits. Deployed as a separate `mm-migrate-dev` Lambda, in the VPC.

The deploy workflow invokes it after `update-function-code` on the payments Lambda — once the binary is up there's no point gating migrations on it because the schema must already be migrated before the new Lambda serves traffic. Order in the deploy job:

1. Build migrate + payments + projector binaries.
2. `update-function-code` for all three.
3. `aws lambda wait function-updated` for all three.
4. `aws lambda invoke mm-migrate-dev` and check the response body for `applied >= 0`.
5. Optional: invoke a smoke-test endpoint on payments.

This deliberately doesn't gate the payments Lambda on a successful migrate — if the migrate Lambda is broken, payments still gets the new code. That's a worse outcome than gating, but it keeps the pipeline simple. Worth revisiting if it ever bites.

---

## 4. Concrete file changes

### 4.1 Add

| Path | Purpose |
|---|---|
| `cmd/projector/main.go` | Kinesis-event Lambda entry point. Init constructs Aurora projection stores; handler decodes records and dispatches. |
| `cmd/migrate/main.go` | One-shot Lambda. Runs `database.Migrate(...)` and returns `{applied: N}`. |
| `internal/app/projector.go` | `BuildProjector(ctx, log, opts) ([]eventstore.Subscriber, error)` — wires Aurora projection stores into projector instances. Reused by both the projector Lambda and (optionally) cmd/server in a future "all-in-one" mode. |
| `internal/projector/dispatcher.go` | Converts a Kinesis record's DDB image into an `eventstore.Event` and looks up the right subscriber. |
| `internal/projector/dispatcher_test.go` | Table-driven tests that take a captured Kinesis record JSON and assert the right subscriber sees the right typed event. |

### 4.2 Modify

| Path | Why |
|---|---|
| `go.mod` / `go.sum` | Add `github.com/aws/aws-lambda-go/events` (already present from Phase 1) and `github.com/jackc/pgx/v5/stdlib` (only if migrate uses `sql.DB`; not strictly required). |
| `internal/app/store.go` | `buildDynamoDeps` keeps event-store wiring. New `buildAuroraQueryStores(ctx) (queryStores, func())` builds Aurora-backed projection stores. `cmd/lambda` calls both. |
| `internal/app/engine.go` | Recognise a fourth implicit mode "dynamodb-with-aurora-reads": when STORE=dynamodb and `AURORA_URL` is set, also build query stores and flip `MountQueryRoutes` on. |
| `internal/app/options.go` | Document the AURORA_URL contract. No new struct fields — the env var is enough. |
| `cmd/lambda/main.go` | If `AURORA_URL` is set, pass `MountQueryRoutes: true`. Otherwise behave like Phase 1. |
| `.github/workflows/deploy.yml` | Build + deploy `mm-projector-dev` and `mm-migrate-dev` in addition to `mm-payments-dev`. Invoke migrate after deploy. |

### 4.3 Leave alone

- `internal/shared/eventstore/dynamodb.go` — unchanged. `Subscribe` stays a no-op; the projector lives outside the store.
- `internal/shared/eventstore/postgres.go` — kept for `cmd/server` local dev.
- All `projections/*postgres_store.go` files — they speak `pgxpool`; Aurora is Postgres. No changes needed.
- `internal/shared/outbox/` — kept until phase 3.
- `internal/payments/recurringmovement/.../processor.go` — kept until phase 3.

---

## 5. Projector Lambda — implementation details

### 5.1 Entry point

```go
// cmd/projector/main.go
package main

import (
    "context"
    "log"
    "log/slog"
    "os"

    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-lambda-go/lambda"

    "github.com/jordisalazar/money-management/internal/app"
    "github.com/jordisalazar/money-management/internal/projector"
)

var dispatch *projector.Dispatcher

func init() {
    if os.Getenv("SKIP_LAMBDA_INIT") == "1" { return }
    log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    d, err := app.BuildProjector(context.Background(), log, app.ProjectorOptions{
        AuroraURL: os.Getenv("AURORA_URL"),
    })
    if err != nil {
        log.Error("build projector", "err", err)
        os.Exit(1)
    }
    dispatch = d
}

func handler(ctx context.Context, e events.KinesisEvent) error {
    return dispatch.HandleBatch(ctx, e.Records)
}

func main() { lambda.Start(handler) }
```

### 5.2 Dispatcher contract

```go
type Dispatcher struct {
    registry    map[string]func() any            // event-type → factory
    subscribers []eventstore.Subscriber
}

func (d *Dispatcher) HandleBatch(ctx context.Context, recs []events.KinesisEventRecord) error {
    for _, r := range recs {
        ev, meta, err := decodeKinesisRecord(r, d.registry)
        if err != nil {
            return fmt.Errorf("record %s: %w", r.EventID, err)
        }
        for _, sub := range d.subscribers {
            sub.Handle(ev, meta)
        }
    }
    return nil
}
```

Failure handling: return error → Kinesis retries the whole batch. The existing projection idempotency rule (deterministic IDs from `meta.EventID`) makes retries safe.

Per-shard concurrency stays at 1; bisect-on-error is enabled in the event source mapping so a single poison record doesn't block a shard forever.

### 5.3 Decoding Kinesis records

The DDB-Kinesis bridge emits JSON of shape:

```json
{
  "awsRegion": "eu-west-1",
  "eventID": "...",
  "eventName": "INSERT",
  "userIdentity": null,
  "tableName": "mm-events-dev",
  "dynamodb": {
    "ApproximateCreationDateTime": 1712345678,
    "Keys": {"aggregate_id": {"S": "..."}, "version": {"N": "1"}},
    "NewImage": { ...full item... },
    "SequenceNumber": "...",
    "SizeBytes": 123
  }
}
```

`decodeKinesisRecord` reads the `NewImage`, runs it through the same `eventFromItem` helper used by `dynamodb.go::Load`, and returns a typed value via the registry factory. We're paying for the JSON round-trip but it's a few hundred bytes per record — negligible.

`INSERT` and `MODIFY` events flow through; `REMOVE` events are unexpected (event store is append-only) and logged + skipped.

### 5.4 Read-model wiring (`BuildProjector`)

```go
func BuildProjector(ctx context.Context, log logger.Logger, opts ProjectorOptions) (*projector.Dispatcher, error) {
    pool, err := pgxpool.New(ctx, opts.AuroraURL)
    if err != nil { return nil, err }

    queries := queryStores{
        wallet:    wprojections.NewPostgresWalletStore(pool),
        movement:  wprojections.NewPostgresMovementStore(pool),
        debt:      dprojections.NewPostgresDebtStore(pool),
        recurring: rmprojections.NewPostgresRecurringMovementStore(pool),
    }
    txStore := txprojections.NewPostgresTransactionStore(pool)

    d := projector.NewDispatcher()
    registerDomainEvents(d) // reuse the helper from internal/app/store.go
    d.Subscribe(wprojections.NewWalletsProjector(queries.wallet))
    d.Subscribe(wprojections.NewMovementsProjector(queries.movement))
    d.Subscribe(txprojections.NewTransactionsProjector(txStore))
    d.Subscribe(dprojections.NewDebtsProjector(queries.debt))
    d.Subscribe(rmprojections.NewRecurringMovementsProjector(queries.recurring))
    return d, nil
}
```

The `registerDomainEvents` helper in `internal/app/store.go` already exists from Phase 1; it just needs to accept anything with a `RegisterEvent(string, func() any)` method. Refactor as needed.

### 5.5 Backfill / first deploy

For a fresh Aurora the projector needs to catch up on every event that landed in DDB before the Kinesis stream existed. Two pragmatic options:

1. **Set Kinesis starting position to `TRIM_HORIZON`** and rely on the stream's retention to cover everything. Works only if the stream's retention ≥ time since Phase 1 deploy. With a 24h retention and a quick Phase 2 follow-up, fine. With longer gaps, set retention to 7 days (paid).
2. **One-shot backfill from DDB Scan**: a manual `aws lambda invoke mm-projector-dev` with a custom payload that triggers `dispatch.Replay()` (calls `store.LoadAll` → dispatches). Only works if `AURORA_URL` is present and the projector knows how to read DDB directly.

Recommendation: option 1 for the first deploy (start retention at 7 days) and decide later whether to bake the backfill path.

---

## 6. Configuration

| Env var | Where | Purpose |
|---|---|---|
| `AURORA_URL` | payments + projector + migrate Lambdas | Postgres connection string. Pulled from Secrets Manager via Terraform `aws_secretsmanager_secret_version` and injected into Lambda env. |
| `EVENTS_TABLE` | payments + projector Lambdas | Existing. Projector reads the table name to validate the Kinesis source. |
| `KINESIS_STREAM` | projector Lambda (event-source mapping) | Configured at the Lambda mapping level, not as a runtime env var. |
| `STORE=dynamodb` | payments Lambda | Existing. |
| `AURORA_URL` empty | If unset on payments Lambda, behaviour matches Phase 1 (GETs off). Allows a partial deploy / rollback. |
| `JWT_SECRET` | payments Lambda | Still env var, will move to Secrets Manager in Phase 4. |
| `ANTHROPIC_API_KEY` | payments Lambda | Optional. If set + AURORA_URL set, voice slice mounts. |

---

## 7. Backfill, replay, and ordering

### 7.1 Per-aggregate ordering

DynamoDB writes the same aggregate's events in version order (the OCC condition ensures this). The Kinesis Data Streams export hashes records by the DDB partition key (`aggregate_id`), so all events for one aggregate land on the same Kinesis shard and Lambda processes them in order. This is exactly the property the projectors need.

### 7.2 Cross-aggregate ordering

There is none. Two events on two different aggregates may be projected out of wall-clock order. None of the current projectors care: each subscribes to event types it cares about and writes idempotent rows keyed by `meta.EventID`. If a future projection needs cross-aggregate ordering (unlikely), we'd add a serial output stream then.

### 7.3 Re-running projections

To rebuild Aurora from scratch:

1. Truncate every read-model table (or drop + recreate the schema).
2. `aws kinesis put-stream-mode` + reset event-source mapping start position to `TRIM_HORIZON`.
3. Re-enable the event source. The projector consumes the entire retained stream.

This is the "in case of bug" path. Combined with idempotent projections it should always be safe.

---

## 8. Testing strategy

### 8.1 Untouched

- Aggregate G/W/T tests, handler tests using in-memory store — all stay green.
- DDB contract tests (`dynamodb_test.go`) — unchanged.

### 8.2 New: Dispatcher unit tests

`internal/projector/dispatcher_test.go`:

| Case | Expected |
|---|---|
| Decode a captured KinesisEventRecord with `eventName: INSERT` and a known event type | Typed value reaches the matching Subscriber with correct `meta.EventID`. |
| Record with unregistered event type | Skipped + logged, no subscriber called, no error returned. |
| Record with `eventName: REMOVE` | Skipped + logged, no error. |
| Record with malformed `NewImage` | Returns wrapped error from HandleBatch (so Kinesis retries). |

No AWS needed — these run on the Go test binary.

### 8.3 New: Aurora projection integration tests

Reuse the existing Postgres projection stores against a local Postgres (the existing docker-compose service is fine). Stand up the dispatcher, feed it captured Kinesis records, assert the projection tables hold the expected rows.

`make test-projector-integration` runs them; skipped in pure-unit CI.

### 8.4 Manual smoke test post-deploy

```sh
# write
curl -X POST $API_URL/wallets -H "Authorization: Bearer $JWT" \
  -d '{"name":"smoke-2","currency":"EUR","category":"Cash","initial_amount":0}'

# wait ~2s for the projector
sleep 2

# read
curl $API_URL/wallets -H "Authorization: Bearer $JWT"
# expect: includes smoke-2
```

The 2 s wait is a hack reflecting projector latency. If it's flaky, increase to 5 s; if it's never flaky, the eventual-consistency story is healthy.

---

## 9. CI / Deploy pipeline

`.github/workflows/deploy.yml` gains two more Lambdas. The job graph:

```
test (LocalStack) ──┐
                     ├─→ deploy-payments ──┐
                     ├─→ deploy-projector ─┼─→ invoke-migrate ─→ smoke-test
                     └─→ deploy-migrate ────┘
```

`build-lambda` Makefile target generalises to take a `CMD` arg:

```makefile
build-lambda: ## Cross-compile $(CMD) (default: lambda) for arm64
CMD ?= lambda
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -tags lambda.norpc -trimpath -ldflags='-s -w' \
		-o bootstrap ./cmd/$(CMD)
	zip -j $(CMD).zip bootstrap
	rm bootstrap
```

CI calls `make build-lambda CMD=lambda`, `CMD=projector`, `CMD=migrate` and uploads each zip.

---

## 10. Execution order within the PR

Each step compiles and tests green at the end.

1. **Add `internal/projector/dispatcher.go` + tests** — no Lambda yet, no Aurora, no Kinesis. Pure decode-and-fanout logic, hermetic tests. *Commit 1.*
2. **Add `internal/app/projector.go` (`BuildProjector`)** — wires Aurora projection stores into the dispatcher. Aurora not required at test time; uses a stub URL when fed `?skip-connect=1` for unit-level wiring tests. *Commit 2.*
3. **Add `cmd/projector/main.go`** + go.mod deps if any. Builds locally; deploy stays manual until commit 6. *Commit 3.*
4. **Add `cmd/migrate/main.go`** — `main` calls `database.Migrate(os.Getenv("AURORA_URL"))` and serves a single `lambda.Start` handler. *Commit 4.*
5. **Update `cmd/lambda/main.go`** to enable `MountQueryRoutes` when `AURORA_URL` is set + build the Aurora projection stores in addition to DDB. *Commit 5.*
6. **Update `.github/workflows/deploy.yml`** to deploy projector + migrate + payments and invoke migrate after deploy. *Commit 6.*
7. **Terraform PR (separate repo)** lands in parallel: VPC, Aurora cluster, Kinesis stream, three Lambda functions, IAM, VPC endpoints. PRs cross-merge once both review clean.

---

## 11. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| VPC cold start for payments Lambda annoys testers | Med | Low | Provisioned concurrency on 1 instance ($-cost). For a toy project, accept 1–3 s on first request. |
| Aurora 0-ACU scale-up adds latency on first read after idle | Med | Low | Pin minimum to 0.5 ACU during active dev, scale back when quiet. Cheap. |
| Projector lags behind writes — GETs look stale | Med | Med | Tune Kinesis batch + concurrency. Smoke test asserts eventual consistency, not strong. Document the lag is ~1–5 s on a healthy projector. |
| Migrations break a Lambda deploy | Med | High | Ship a no-op migration first to verify the runner end-to-end. After that the failure mode is "next migration fails" which surfaces in CI before traffic hits payments. |
| Aurora connection storms on cold start | Low | Med | `pgxpool` defaults to 4 conns max. Multiple Lambda containers ⇒ multiple pools. Cap Lambda reserved concurrency at 10 to bound pool count. |
| Kinesis retention rolls events off before projector catches up | Low | High | Set retention to 7 days. Alarm on `IteratorAge > 1h`. |
| One bad projector commit corrupts read models | Low | Med | Idempotent projections + the "truncate and replay" recovery path. Rehearse it once. |
| Lambda env var size limit (4 KB total) | Low | Low | Same as Phase 1. Still far below. |
| VPC interface endpoints accrue silent cost | Med | Low | $0.01/hr each × 3 endpoints × 24 × 30 ≈ $22/mo. Worth knowing about. Drop the SSM endpoint if we don't use Parameter Store. |

---

## 12. Out of scope (explicit)

- Cognito + API GW JWT authorizer (Phase 4).
- Reactor Lambdas (Phase 3).
- EventBridge cron for recurring movements (Phase 3).
- Deletion of `postgres.go`, the Postgres event-store migrations, the outbox table (Phase 3, once reactors land).
- CORS configuration on API GW (Phase 4 with the frontend).
- X-Ray trace propagation through `correlation_id`.
- Frontend deployment (Phase 4).
- CloudWatch dashboards (Phase 5).
- Aurora point-in-time restore policy / backup retention tuning.
- Read replicas on Aurora.

---

## 13. Future phases — unchanged from the overall plan

[Phase 3](./aws-migration-plan.md#phase-3-reactors--scheduled-jobs), [Phase 4](./aws-migration-plan.md#phase-4-cognito--frontend--hardening), [Phase 5](./aws-migration-plan.md#phase-5-maybe-observability-uplift) keep the shape sketched in the master plan.

---

## 14. Open questions for review

These are decisions I want a yes/no on before starting Phase 2:

1. **Kinesis Data Streams over DynamoDB Streams direct-to-Lambda.** Costs slightly more, buys multi-consumer fan-out we'll want in Phase 3. Y/N?
2. **Aurora Serverless v2 vs RDS Postgres** (provisioned db.t4g.micro is ~$15/mo flat, Aurora idles cheaper but ACU bursts cost more under load). For a toy project the bursty workload is irrelevant — recommend Aurora for the simpler scale-to-near-zero. Y/N?
3. **Single NAT + VPC endpoints for DDB/Kinesis** vs no NAT + VPC endpoints for everything. The endpoints exist for both. NAT is ~$32/mo + data charges; endpoints are ~$22/mo for 3 interface endpoints + free for DDB/S3. With endpoints covering DDB, Kinesis, Secrets Manager I think we can skip NAT entirely. Y/N to skip NAT?
4. **Same payments Lambda for POST + GET** in VPC, or split write-Lambda (DDB-only, no VPC, fast cold start) + read-Lambda (Aurora, VPC, slow cold start). Single Lambda is simpler and matches the existing routing. Y/N to single Lambda?
5. **Migrations runner as a dedicated Lambda** vs invoked from the GitHub Actions runner via a bastion / Session Manager tunnel. Lambda is simpler. Y/N to dedicated `mm-migrate-dev` Lambda?
6. **`cmd/lambda` reads `AURORA_URL` to flip on `MountQueryRoutes`** rather than adding a new `Options.MountAurora` field. Env-var-driven config keeps the cmd entry points lean. Y/N?
7. **Backfill via Kinesis TRIM_HORIZON** (set retention to 7 days, accept that as the "you must deploy Phase 2 within 7 days" SLA) vs build a one-shot DDB-Scan backfill path now. Recommend Kinesis-only for first deploy. Y/N?
8. **Voice slice auto-enables** once `MountQueryRoutes` flips true on Lambda. The `ANTHROPIC_API_KEY` gate stays. Y/N?
9. **Aurora connection string in Secrets Manager** (rotation + audit log) vs SSM Parameter Store (cheaper, no rotation). For a toy project SSM is fine. Y/N to SSM?
10. **Drop `STORE=postgres` mode from `cmd/server`** after Phase 2 lands, or keep it as a "what would Railway look like" reference? Plan §15.2 of the original deferred this — Phase 2 is the natural drop point. Y/N to drop after this PR?
11. **Postgres event store + outbox tables stay in migrations** until Phase 3, or remove now (Aurora doesn't need them). Removing now means cmd/server's local Postgres also loses them — fine if STORE=postgres goes away (Q10). Y/N to remove together with Q10?

---

## 15. Glossary additions

- **Aurora Serverless v2** — Postgres-compatible managed DB that scales up/down in fine-grained ACU units. Scales to zero when idle (cheap).
- **ACU** — Aurora Capacity Unit. 1 ACU ≈ 2 GB memory + proportional CPU.
- **Kinesis Data Streams export** — DDB feature that pushes every committed write to a Kinesis stream in near real time.
- **VPC endpoint (Gateway)** — Free per-service shortcut for DDB / S3 traffic from within a VPC. No NAT needed.
- **VPC endpoint (Interface)** — Per-AZ ENI for accessing other AWS services privately. Costs ~$0.01/hr per endpoint per AZ.
- **TRIM_HORIZON** — Kinesis stream position pointing at the oldest retained record.

---

**End of plan. Mark up directly with comments, or send replies via the questions in §14.**
