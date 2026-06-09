# AWS Migration Plan — Backend, Phase 3

**Status**: Draft for review
**Last updated**: 2026-06-09
**Reviewers**: jordi
**Phase**: 3 of 4 (Reactor Lambda + EventBridge cron)

Phase 1 (DDB store + Lambda) landed at `08e0045`. Phase 2 (projector + Aurora) landed at `26fd9f3`. This document covers Phase 3: bringing the dormant reactors back online and replacing the deleted recurring-movement processor loop with an EventBridge schedule.

---

## 1. Context

Two effects regressed when Phase 1+2 stripped Postgres:

- **Debt settlement no longer moves money.** `settledebt.CommandHandler` writes the `DebtSettled` event, but the reactor that turned that into a wallet deposit/withdrawal lost its trigger when the outbox poller was deleted. The reactor function itself (`internal/payments/debt/settledebt/reactor.go`) still exists with the right business logic — it's just not wired to anything.
- **Recurring movements no longer execute.** `processor.go` was deleted in Phase 2 because it was Postgres-pool coupled. The "find due items today and dispatch Execute commands" loop has no caller anymore.

Phase 3 fixes both by adding two new Lambdas, both consuming AWS event sources rather than poll loops.

## 2. Goal of Phase 3

End-state after Phase 3 merges and deploys:

- A **reactor Lambda** (`mm-reactor-dev`) consumes the same Kinesis Data Stream the projector uses. It filters for `DebtSettled` and `RecurringMovementExecuted` events and invokes the existing settledebt + recurring reactor business logic — which then issues deposit/withdraw commands back into DynamoDB.
- A **recurring-cron Lambda** (`mm-recurring-cron-dev`) runs on an EventBridge schedule, queries Aurora's `recurring_movements_view` for items where `next_due_date <= today AND active = true`, and issues an `ExecuteRecurringMovement` command per item.
- Reactor processing is idempotent against Kinesis retries via a DDB **inbox table** (`mm-reactor-inbox-dev`) keyed on `source_event_id`.
- The `outbox` types package can finally be deleted: the dormant reactors get rewritten to take typed events directly.

What's deliberately deferred:

- **Cognito + API GW JWT authorizer** — Phase 4.
- **Frontend** — Phase 4.
- **Observability uplift** — Phase 5.

---

## 3. Architectural shifts

### 3.1 Reactor Lambda — Kinesis consumer

The Phase 2 Kinesis stream already delivers every DDB-written event. The projector Lambda subscribes for read-model writes; the reactor Lambda subscribes for downstream commands.

Both consumers attach to the **same** stream — Kinesis Data Streams' multi-consumer fan-out was the explicit reason we picked it over raw DDB Streams (Phase 2 §3.1, Q1). Each consumer gets its own iterator; back-pressure on one doesn't affect the other.

Routing: the reactor Lambda decodes each record using the projector dispatcher's existing helpers (`internal/projector/dispatcher.go`'s `kinesisDDBRecord` decoder is generalisable), then dispatches by `event_type` to typed handler functions.

Filtering happens **in code, not at the event-source mapping**. The Kinesis filter expression syntax is rigid (matches against the wrapping record envelope, not the embedded DDB image), and the cost of decoding then skipping a record we don't care about is negligible — one map lookup per record.

### 3.2 Recurring-movement cron Lambda

An **EventBridge Schedule** (cron expression) fires `mm-recurring-cron-dev` every N hours. Default cadence proposed: every 6 hours (00:00, 06:00, 12:00, 18:00 UTC). A toy project doesn't need minute-precision timing.

The Lambda:

1. Opens a `pgxpool` to Aurora (in VPC, so it needs the same subnet config as the projector).
2. Queries `recurring_movements_view WHERE next_due_date <= CURRENT_DATE AND active = true`.
3. For each row, calls `executerecurringmovement.CommandHandler.Handle(ctx, Command{RecurringMovementID: row.ID, ExecutedForDate: today})`.
4. Swallows `ErrAlreadyExecutedToday` and `ErrNotActive` (expected idempotency / state guards).
5. Returns the count processed.

The Execute command writes a `RecurringMovementExecuted` event to DDB → Kinesis → the reactor Lambda picks it up → wallet deposit/withdraw command runs. The cron Lambda doesn't move money directly; it just starts the chain.

### 3.3 Reactor idempotency — at the domain layer

Kinesis is **at-least-once**. A reactor that issues a deposit on every delivery would double-spend on retries.

The chosen pattern (Q3 decision: Option C in the deep-dive review): the wallet aggregate becomes idempotent on a per-event basis. Each `MoneyDeposited` / `MoneyWithdrawn` event carries an optional `IdempotencyKey string`. The aggregate's `Apply()` adds every non-empty key to a tracked set. The `Deposit()` / `Withdraw()` methods short-circuit with `ErrAlreadyProcessed` when the supplied key is already in the set.

The reactor passes the source event's `event_id` as the idempotency key when invoking deposit/withdraw. On retry, the second call hits the short-circuit and returns nil → the Kinesis record drains, no double-spend, no manual recovery needed.

Why this over an inbox table:

- **No new infra.** No `mm-reactor-inbox-dev` table, no extra IAM, no extra config.
- **No race window.** The check + write happen inside the same `Append` call. OCC handles concurrent reactors: the loser's retry sees the winner's event carrying the matching key and skips.
- **Domain-layer correctness.** The "have I already done X" question lives in the aggregate where invariants belong, not in infrastructure where it would drift.

HTTP-triggered deposits/withdrawals pass an empty `IdempotencyKey` — the field is optional, and an empty key is never tracked (the set ignores `""`). Backwards compatible.

### 3.4 Reactor input shape refactor

Today the dormant reactors take `outbox.Entry`:

```go
func(ctx context.Context, entry outbox.Entry) error
```

That shape was an artefact of the in-database outbox row format. With Phase 3 the input is a Kinesis record, which the dispatcher already decodes into a typed value. Switching the reactor signature to:

```go
func(ctx context.Context, event TypedEvent, meta eventstore.EventMetadata) error
```

removes a JSON round-trip (the reactor currently unmarshals `entry.Payload`) and removes the dependency on the `outbox` types package, which can then be deleted.

Each reactor becomes one function per event type: `HandleDebtSettled(debt.DebtSettled, meta)`, `HandleRecurringMovementExecuted(recurringmovement.RecurringMovementExecuted, meta)`. The reactor Lambda has a tiny router that picks the function by event type.

---

## 4. Concrete file changes

### 4.1 Add

| Path | Purpose |
|---|---|
| `cmd/reactor/main.go` | Kinesis-event Lambda. Init wires the typed reactors + the inbox; handler decodes each record and dispatches. |
| `cmd/recurring-cron/main.go` | EventBridge-triggered Lambda. Queries Aurora, dispatches Execute commands. |
| `internal/app/reactor.go` | `BuildReactor(ctx, log, opts) (*ReactorBundle, error)` — opens DDB client + event store + inbox, wires every typed reactor handler with idempotency. |
| `internal/reactor/router.go` | `Router.HandleBatch(ctx, []KinesisEventRecord) error` — decodes records, dispatches to typed handlers. Idempotency lives in the aggregate, not here. |
| `internal/reactor/router_test.go` | Table-driven dispatcher tests with a fake inbox + fake handlers. |

### 4.2 Modify

| Path | Why |
|---|---|
| `internal/payments/debt/settledebt/reactor.go` | Replace `func(ctx, outbox.Entry) error` with `HandleDebtSettled(ctx, debt.DebtSettled, eventstore.EventMetadata) error`. Drop the JSON unmarshal — input is already typed. |
| `internal/payments/debt/settledebt/reactor_test.go` | Update to pass a typed event directly. |
| `internal/payments/recurringmovement/executerecurringmovement/reactor.go` | Same refactor — typed input. |
| `internal/payments/recurringmovement/executerecurringmovement/reactor_test.go` | Same update. |
| `internal/projector/dispatcher.go` | Extract the `kinesisDDBRecord` decoder + `ddbImage` helper into a small shared package (`internal/projector/kinesisdecode`) so the reactor router can reuse it without circular imports. |
| `.github/workflows/deploy.yml` | Build + deploy `mm-reactor-dev` and `mm-recurring-cron-dev`. |
| `Makefile` | `build-all-lambdas` builds five zips now. |

### 4.3 Delete

| Path | Why |
|---|---|
| `internal/shared/outbox/types.go` | `outbox.Entry` / `outbox.Handler` types no longer referenced once the reactors take typed events. |
| `internal/shared/outbox/` (entire dir) | Empty after types.go is gone. |

---

## 5. Reactor Lambda — implementation details

### 5.1 Entry point

```go
// cmd/reactor/main.go
package main

import (
    "context"
    "log"
    "os"

    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-lambda-go/lambda"

    "github.com/jordisalazar/money-management/internal/app"
)

var bundle *app.ReactorBundle

func init() {
    if os.Getenv("SKIP_LAMBDA_INIT") == "1" { return }
    b, err := app.BuildReactor(context.Background(), nil, app.ReactorOptions{})
    if err != nil { log.Fatalf("build reactor: %v", err) }
    bundle = b
}

func handler(ctx context.Context, e events.KinesisEvent) error {
    return bundle.Router.HandleBatch(ctx, e.Records)
}

func main() { lambda.Start(handler) }
```

### 5.2 Router contract

```go
type Router struct {
    decoder kinesisdecode.Decoder
    inbox   *Inbox
    log     *slog.Logger

    debt      func(ctx context.Context, e debt.DebtSettled, meta eventstore.EventMetadata) error
    recurring func(ctx context.Context, e recurringmovement.RecurringMovementExecuted, meta eventstore.EventMetadata) error
}

func (r *Router) HandleBatch(ctx context.Context, recs []events.KinesisEventRecord) error {
    for _, rec := range recs {
        if err := r.dispatchOne(ctx, rec); err != nil {
            return fmt.Errorf("record %s: %w", rec.EventID, err)
        }
    }
    return nil
}
```

Per-record dispatch:

1. Decode the record (skip REMOVE / unknown event types per the same rules as the projector).
2. Look at `event_type`. If it's not in the routing table → log debug, skip.
3. Pull `event_id` from the decoded image → that's the `source_event_id`.
4. `r.inbox.Once(ctx, source_event_id)` → if `firstTime == false`, log and skip.
5. Decode the payload into the typed event.
6. Call the matching reactor function.
7. Any error returned from the reactor bubbles up → Kinesis retries the batch.

### 5.3 Error classification

The reactor functions can fail for two reasons:

- **Retryable**: DynamoDB throttling, transient network errors, OCC conflicts on the deposit/withdraw write (someone else touched the wallet between our Load and Append). Return the error → Kinesis retries.
- **Permanent**: wallet doesn't exist, wallet is disabled, currency mismatch. Returning these would loop forever. Log loud and return nil.

A small `func isPermanent(err error) bool` checks against the sentinel errors exported by the wallet aggregate. Easy to extend later.

### 5.4 IAM permissions

The reactor Lambda needs:

- `kinesis:GetRecords`, `kinesis:GetShardIterator`, `kinesis:DescribeStream` on the events stream.
- `dynamodb:PutItem`, `dynamodb:GetItem` on `mm-reactor-inbox-dev`.
- `dynamodb:GetItem`, `dynamodb:PutItem`, `dynamodb:TransactWriteItems`, `dynamodb:Query` on `mm-events-dev` (the command handlers run Load + Append).

---

## 6. Cron Lambda — implementation details

### 6.1 Entry point

```go
// cmd/recurring-cron/main.go
package main

import (
    "context"
    "log"
    "os"

    "github.com/aws/aws-lambda-go/lambda"

    "github.com/jordisalazar/money-management/internal/app"
)

var bundle *app.RecurringCronBundle

func init() {
    if os.Getenv("SKIP_LAMBDA_INIT") == "1" { return }
    b, err := app.BuildRecurringCron(context.Background(), nil, app.RecurringCronOptions{})
    if err != nil { log.Fatalf("build recurring cron: %v", err) }
    bundle = b
}

type Response struct {
    DispatchedCount int      `json:"dispatched_count"`
    SkippedCount    int      `json:"skipped_count"`
    Failures        []string `json:"failures,omitempty"`
}

func handler(ctx context.Context) (Response, error) {
    return bundle.Run(ctx)
}

func main() { lambda.Start(handler) }
```

### 6.2 Bundle contract

`BuildRecurringCron` opens:

- A `pgxpool` against `AURORA_URL` (read-only — the cron only queries the view).
- A `DynamoStore` against `EVENTS_TABLE` (the command handler writes through it).

`Run(ctx)` does:

```sql
SELECT id FROM recurring_movements_view
WHERE next_due_date <= CURRENT_DATE AND active = true
```

For each id: dispatch the `ExecuteRecurringMovement` command. On `ErrAlreadyExecutedToday` or `ErrNotActive`, increment `SkippedCount`. On any other error, append to `Failures` and continue (don't fail-fast — one bad row shouldn't stop the others).

Returning the count + failures lets CI or a manual `aws lambda invoke` see what happened.

### 6.3 Schedule

EventBridge rule `rate(6 hours)` or `cron(0 0,6,12,18 * * ? *)`. Both fire at predictable times; cron expression makes alerting on missed fires easier. Recommend cron.

### 6.4 What happens at end-of-day boundaries

If a movement's `next_due_date` is `2026-06-09` and the cron fires at 00:00 UTC on 2026-06-10, the `<= CURRENT_DATE` filter still matches. So missing one cron firing doesn't lose the execution.

If the cron has been off for a week and a movement was due 6 days ago, today's run dispatches it for today's `ExecutedForDate`. The aggregate's invariant updates `next_due_date` after each execution, so the next run picks up the next due-date. Catch-up is naturally bounded.

---

## 7. Idempotency design — Inbox

### 7.1 Table

`mm-reactor-inbox-dev`:

| Attr | Type | Notes |
|---|---|---|
| `source_event_id` | S (PK) | The DDB event's `event_id` attribute — the same UUIDv7 we use throughout. |
| `processed_at` | S | ISO-8601 UTC. Informational; not read by the reactor. |
| `event_type` | S | Informational; helps when debugging. |
| `ttl` | N | Unix timestamp 7 days out. DDB TTL evicts expired rows automatically. |

PAY_PER_REQUEST billing. Single-table, single-key — minimal infra.

### 7.2 Inbox.Once

```go
func (i *Inbox) Once(ctx context.Context, sourceEventID, eventType string) (bool, error) {
    _, err := i.client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: aws.String(i.tableName),
        Item: map[string]types.AttributeValue{
            "source_event_id": &types.AttributeValueMemberS{Value: sourceEventID},
            "event_type":      &types.AttributeValueMemberS{Value: eventType},
            "processed_at":    &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
            "ttl":             &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(7*24*time.Hour).Unix(), 10)},
        },
        ConditionExpression: aws.String("attribute_not_exists(source_event_id)"),
    })
    if err == nil {
        return true, nil // first time we've seen this event
    }
    var ccfe *types.ConditionalCheckFailedException
    if errors.As(err, &ccfe) {
        return false, nil // already processed
    }
    return false, fmt.Errorf("inbox PutItem: %w", err)
}
```

### 7.3 Race window — explicit

If the reactor crashes after `Inbox.Once` returns `firstTime=true` but before the deposit/withdraw command commits, the retry will see `firstTime=false` and skip. Worst case: a missing wallet movement.

Recovery procedure (document in §13):

```sh
# Find the inbox entry
aws dynamodb scan --table-name mm-reactor-inbox-dev \
  --filter-expression 'source_event_id = :id' \
  --expression-attribute-values '{":id":{"S":"<event_id>"}}'

# Delete it, then re-trigger the reactor by invoking with a synthetic payload
# (or wait for the next Kinesis trim-horizon replay)
aws dynamodb delete-item --table-name mm-reactor-inbox-dev \
  --key '{"source_event_id":{"S":"<event_id>"}}'
```

---

## 8. Configuration

| Env var | Lambdas | Purpose |
|---|---|---|
| `EVENTS_TABLE` | reactor, recurring-cron | Existing. Reactor's command handlers write here. |
| `REACTOR_INBOX_TABLE` | reactor | New. Default `mm-reactor-inbox-dev` if unset. |
| `AURORA_URL` | recurring-cron | Existing. Cron queries the view. |
| `AWS_REGION` | both | Provided by runtime. |

The reactor Lambda does **not** need `AURORA_URL` — it doesn't read read-models. Stays out of VPC, fast cold start.

The cron Lambda **does** need VPC because it talks to Aurora. Slower cold start, acceptable because it's invoked on a schedule (no user latency dependence).

---

## 9. Testing strategy

### 9.1 Untouched

- DDB contract tests (unchanged).
- Projector dispatcher tests (unchanged; just internal package moves).
- Aggregate G/W/T tests.

### 9.2 New: Inbox contract tests

`internal/reactor/inbox_test.go` — same LocalStack pattern as `dynamodb_test.go`:

| Case | Expected |
|---|---|
| `Once` on a fresh `source_event_id` | `firstTime=true`, item appears |
| `Once` on the same id twice | second call returns `firstTime=false` |
| `Once` with a different id after first | `firstTime=true` |
| TTL attribute set ~7 days out | within tolerance |

### 9.3 New: Router unit tests

`internal/reactor/router_test.go` — fully hermetic, no AWS:

- Captured Kinesis record + fake inbox always returning `firstTime=true` → matching handler is called once with the typed event.
- Fake inbox returning `firstTime=false` → handler not called, no error.
- Record with unrouted event_type → no handler called, no error.
- Inbox returning an error → router returns error.

### 9.4 New: Cron tests

`cmd/recurring-cron/main_test.go` (or in `internal/app/recurring_cron_test.go`) — fake the `pgxpool` Query, fake the command handler, assert the right ids get dispatched and the right errors get swallowed.

### 9.5 Manual smoke test post-deploy

```sh
# 1. Trigger a debt settlement, watch the wallet balance update ~2 s later
curl -X POST $API_URL/debts/$DEBT_ID/settle -H "Authorization: Bearer $JWT"
sleep 2
curl $API_URL/wallets -H "Authorization: Bearer $JWT" | jq '.wallets[] | {id, balance}'
# expect: wallet associated with the debt has the expected balance change

# 2. Trigger the cron Lambda manually, check the response
aws lambda invoke --function-name mm-recurring-cron-dev /tmp/out.json
cat /tmp/out.json
# expect: {"dispatched_count": N, "skipped_count": M, "failures": []}
```

---

## 10. CI / Deploy pipeline

`.github/workflows/deploy.yml` gains two more deploy steps. The job graph:

```
test (LocalStack) ──┐
                     ├─→ deploy payments
                     ├─→ deploy projector
                     ├─→ deploy migrate
                     ├─→ deploy reactor          (new)
                     ├─→ deploy recurring-cron   (new)
                     └─→ invoke migrate ─→ optional smoke
```

`Makefile`:

- `build-all-lambdas` builds all five zips.
- New convenience targets `build-reactor`, `build-recurring-cron`.

---

## 11. Execution order within the PR

Each step keeps the tree green.

1. **Refactor projector decoder into `internal/projector/kinesisdecode`** — shared by projector + reactor. Pure code move. *Commit 1.*
2. **Add `internal/reactor/inbox.go`** + contract tests. Self-contained DDB module. *Commit 2.*
3. **Refactor the dormant reactors to take typed events.** Update the existing reactor_test.go files. Delete `internal/shared/outbox/` after. *Commit 3.*
4. **Add `internal/reactor/router.go`** wiring inbox + typed handlers. Hermetic tests. *Commit 4.*
5. **Add `internal/app/reactor.go` + `cmd/reactor/main.go`**. Cross-compile check. *Commit 5.*
6. **Add `internal/app/recurring_cron.go` + `cmd/recurring-cron/main.go`** + tests. *Commit 6.*
7. **Update Makefile + deploy workflow** for the two new Lambdas. *Commit 7.*

Total: ~7 small commits in one PR. ~700 LOC added, ~150 LOC moved, ~80 LOC deleted (outbox package).

---

## 12. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Reactor crashes after inbox PutItem but before issuing command → lost wallet move | Low | Med | Document recovery (§7.3). Toy project; manual reconciliation acceptable. |
| Reactor double-issues a deposit/withdraw if inbox PutItem succeeds twice somehow | Very low | Med | Inbox PutItem is conditional with `attribute_not_exists`. DDB guarantees serialisability for conditional writes. |
| Cron misses a firing → next firing catches up | Med | Low | Filter is `<= CURRENT_DATE`, so missed days are still picked up. Aggregate invariant prevents double-execution per day. |
| Cron fires on Aurora cold start → 30 s pool-init plus 1–3 s VPC cold start | Med | Low | EventBridge tolerates Lambda startup. Schedule isn't latency-sensitive. |
| Permanent errors loop forever (wallet disabled, etc.) | High if not handled | Med | `isPermanent(err)` classifier returns nil for sentinel errors so the record drains. |
| Kinesis retention rolls events off before reactor catches up | Low | High | Same as projector — 7-day retention from Phase 2. CloudWatch alarm on `IteratorAge`. |
| Inbox table grows unboundedly without TTL | Low | Low | TTL attribute set at write time; DDB native TTL evicts within ~48 h of expiry. |
| Two Kinesis consumers (projector + reactor) compete for shard throughput | Low | Low | Single shard at our volume. Adding consumers doesn't reduce throughput per Lambda. |
| Reactor IAM policy mistakenly omitted `dynamodb:Query` on events table → reactor can write but not Load | Med (during setup) | High | Smoke test (§9.5) catches it because the deposit command does a Load. |

---

## 13. Out of scope (explicit)

- Cognito + API GW JWT authorizer (Phase 4).
- Frontend deployment (Phase 4).
- CORS configuration on API GW (Phase 4).
- Secrets Manager rotation for `JWT_SECRET` (Phase 4).
- Per-reactor Lambda split (Phase 5 if needed for blast-radius isolation).
- Dead-letter queue wiring (Phase 5; permanent errors are dropped + logged for now).
- CloudWatch dashboards / alarms (Phase 5).
- Audit trail for reactor invocations (Phase 5).

---

## 14. Future phases — unchanged

[Phase 4](./aws-migration-plan.md#phase-4-cognito--frontend--hardening) and [Phase 5](./aws-migration-plan.md#phase-5-maybe-observability-uplift) keep the shape from the master plan.

---

## 15. Open questions for review

Yes/no on each before I start.

1. **Single reactor Lambda routing by event_type** vs **one Lambda per reactor**. Single is simpler ops + cheaper but couples blast radius. Y/N to single?
2. **Reactor input shape: typed events** (drop `outbox.Entry`, delete `internal/shared/outbox`). Y/N?
3. **Idempotency: DDB inbox table with inbox-first semantics** vs alternatives (idempotency key in command, action-first inbox). Y/N to inbox-first?
4. **Inbox TTL of 7 days** (matches Kinesis retention from Phase 2 §6). Y/N?
5. **Cron cadence: `cron(0 0,6,12,18 * * ? *)` (every 6 hours)** vs `rate(1 hour)` vs daily. Recommend 6 hours for a toy project. Y/N to 6 hours?
6. **Permanent-error classification: return nil + log for sentinel domain errors** (wallet disabled, currency mismatch, etc.) so the record drains. Y/N?
7. **Reactor stays out of VPC**, cron stays in VPC (Aurora reach). Y/N?
8. **No DLQ in Phase 3** — permanent errors get logged and dropped. Add DLQ in Phase 5 with the rest of the observability uplift. Y/N?
9. **Filter Kinesis records in code, not at the event-source mapping.** Cheap and avoids the awkward filter expression syntax. Y/N?
10. **Delete `internal/shared/outbox/` entirely once the reactor refactor lands.** Y/N?

---

## 16. Glossary additions

- **Inbox pattern** — Idempotency check that records "I've processed event X" before doing the work, so retries can skip. Pairs with the outbox pattern (which we removed in Phase 2).
- **EventBridge Schedule** — AWS cron service. Cron or rate expression triggers a target (Lambda, here).
- **Per-shard concurrency** — Lambda's Kinesis event source mapping setting that caps how many concurrent invocations process records from one shard. We keep it at 1 to preserve ordering.

---

**End of plan. Mark up directly with comments, or send replies via the questions in §15.**
