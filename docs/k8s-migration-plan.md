# Kubernetes Migration Plan — Backend

**Status**: Draft for review
**Last updated**: 2026-09-15
**Reviewers**: jordi
**Phases**: 6 (phases 1–4 detailed here; 5–6 sketched)
**Decisions**: [ADR 0001](./adr/0001-event-schema-versioning-and-upcasting.md) ·
[ADR 0002](./adr/0002-cross-aggregate-processes.md) ·
[ADR 0003](./adr/0003-independent-projection-subscriptions.md) ·
glossary in [CONTEXT.md](../CONTEXT.md)

---

## 1. Context

The backend currently runs on AWS: Lambda behind API Gateway, DynamoDB as the event store, a Kinesis stream feeding a projector Lambda and a reactor Lambda, Aurora Serverless for projections, EventBridge for the recurring-movement schedule, Cognito for auth. That topology landed across the four phases documented in `aws-migration-plan.md`.

We're moving to Kubernetes, run locally, primarily to learn Kubernetes:

- **Event store**: DynamoDB → KurrentDB (formerly EventStoreDB)
- **Projections**: Aurora Serverless → PostgreSQL (StatefulSet)
- **Runtime**: Lambda × 5 → Deployments + CronJobs + a Job
- **Event delivery**: DynamoDB Streams → Kinesis → Lambda → KurrentDB subscriptions
- **Edge**: API Gateway + CloudFront → Ingress (nginx)
- **Auth**: API Gateway JWT authorizer → in-process OIDC middleware (phase 5)

Go and React stay. The vertical-slice structure, the aggregates, and the projection code stay.

Two things that are *not* infrastructure ride along, because the migration forces both and neither is safe to defer:

- **Event schema evolution**, handled from the first commit rather than retrofitted (§3).
- **Cross-aggregate processes and their failure modes** (§4). This is where the two live bugs in §4.1 get fixed.

### 1.1 Why this is not a like-for-like port

Four properties of the AWS topology have no direct equivalent and force real design decisions:

1. **`TransactWriteItems` gave atomic writes across aggregates.** KurrentDB has no multi-stream transaction. This is the single biggest change in the migration — see §4.
2. **Lambda + Kinesis gave managed at-least-once delivery with checkpointing.** We now own subscription position and checkpoint durability ourselves.
3. **API Gateway validated JWTs before the code ran.** Behind an Ingress, nothing does. The current `CognitoAuth` middleware trusts claims it is handed; it does not verify signatures.
4. **Nothing in the codebase records which shape an event was written with.** Event types are bare strings, payloads are Go structs, and seven hand-written `switch` statements decode them. A fresh store is the only moment at which versioning can be introduced without already having unversioned history to reason about.

### 1.2 What survives untouched

Worth stating explicitly, because it's most of the codebase:

- Every aggregate invariant and its Given/When/Then tests. The aggregates gain methods (`wallet.Refund`); they do not lose any.
- Every HTTP handler and the `RegisterCommands` / `RegisterQueries` wiring shape.
- The projection code and the `eventstore.Subscriber` fan-out interface.
- Deterministic projection row IDs from `meta.EventID` — the property that makes replay safe. This becomes *more* important, not less.
- The reactor's permanent-vs-retryable `classify()` split, which maps cleanly onto KurrentDB persistent-subscription `Ack` / `Nack(Park)` / `Nack(Retry)`. The *classification* is right; what happens on a permanent error is not (§4.1).

---

## 2. Target topology

```
                            Ingress (nginx)
                             /            \
                 /  → web Deployment      /api → api Deployment (N replicas)
                        (nginx + dist)             │
                                                   │ append / read stream
                                                   ▼
                                    KurrentDB StatefulSet + PVC
                                                   │
                          ┌────────────────────────┴─────────────────────┐
                          │ 5 catch-up subs on $all     persistent sub on $all
                          ▼                                              ▼
                 projector Deployment                          reactor Deployment
                 (replicas: 1, Recreate)                       (park / retry / ack)
                          │                                              │
                          │ rows + own checkpoint, one tx        commands → KurrentDB
                          │ per projection                                │
                          ▼                                               │
                 PostgreSQL StatefulSet + PVC ◄────────────────────────────┘
                          ▲
                          │ reads recurring_movements_view / processes_view
        recurring-cron CronJob (daily)  ·  sweeper CronJob  ·  migrate Job (Helm hook)
```

Two different subscription models, deliberately:

| | Projector | Reactor |
|---|---|---|
| Subscription | One catch-up on `$all` **per projection** | Persistent on `$all` |
| Checkpoint owner | Us, in Postgres, one row per projection | KurrentDB (server-side) |
| Replicas | 1 (ordering matters) | 1+ (competing consumers OK) |
| Delivery semantics | Effectively exactly-once (checkpoint commits in the same tx as that projection's rows) | At-least-once + idempotency keys |
| Failure handling | Crash and resume from that projection's checkpoint | `Nack(Park)` → poison queue, `Nack(Retry)` → redeliver |

Projections need total ordering and idempotent replay, so a catch-up subscription with a checkpoint we control is right. Reactors issue commands, need per-message retry and a dead-letter path, and are already idempotent by construction — persistent subscriptions give all three for free.

**Each projection owns its own subscription and checkpoint row** rather than all five advancing in lockstep — see [ADR 0003](./adr/0003-independent-projection-subscriptions.md). The reason is rebuild cost: §3 commits us to upcasting, and adding an upcaster means replaying the projections it affects. Independently checkpointed, that is *truncate one table, delete one checkpoint row, restart*, while the other four keep serving. The cost is that views sit at slightly different positions, so a screen can briefly show a balance that includes a movement not yet in its list. It self-corrects in milliseconds and no invariant depends on the two agreeing.

Two CronJobs, not one. `recurring-cron` fires due occurrences; the **sweeper** finds processes that have stopped making progress and re-drives them (§4.5).

---

## 3. Event evolution

Full rationale in [ADR 0001](./adr/0001-event-schema-versioning-and-upcasting.md). Summary of what gets built.

### 3.1 The seam ships empty

The store starts empty, so on day 1 there is nothing to upcast. What ships is the *seam*: every event carries a version from v1, there is one place upcasters would be registered, and there are golden fixtures proving the current shapes. The registry itself has **zero entries**. That is the point — the machinery exists before it is needed, so the first real schema change is a five-line addition rather than a retrofit against live history.

### 3.2 Version lives in metadata

The event type name is stable forever (`"MoneyDeposited"`, never `"MoneyDeposited.v2"`). The version goes into KurrentDB event metadata alongside the fields §5.3 already moves there:

```
eventType: "MoneyDeposited"
data:      { "wallet_id": ..., "amount": ..., "category": ... }
metadata:  { "schema_version": 2, "correlation_id": ..., "occurred_at": ..., "aggregate_type": "wallet" }
```

The type-name-suffix convention is more common in the EventStoreDB world and is self-describing in the UI, but every routing table in this codebase is keyed by the bare type string — `registerDomainEvents` in `internal/app/store.go`, the reactor's `switch eventType`, the projector dispatcher. A suffix would fragment each of those per version, or require a strip-the-suffix helper, which is metadata-versioning with extra steps. It would also split `$et-MoneyDeposited` into a system projection per version.

### 3.3 One codec, replacing seven decode switches

`eventstore.Codec` owns the type registry *and* the upcaster chains, and is the only place a raw payload is turned into a typed value. It is used by both sides: aggregate rehydration on the write side, projector and reactor on the read side.

This deletes seven duplicated private decode switches:

| File | Symbol |
|---|---|
| `wallet/slices/depositmoney/command_handler.go` | `unmarshalEvent` (:64) |
| `wallet/slices/withdrawmoney/command_handler.go` | `unmarshalEvent` (:64) |
| `wallet/slices/disablewallet/command_handler.go` | `unmarshalEvent` (:62) |
| `recurringmovement/slices/stoprecurringmovement/command_handler.go` | `unmarshalEvent` (:67) |
| `recurringmovement/slices/executerecurringmovement/command_handler.go` | `unmarshalEvent` (:66) |
| `debt/slices/settledebt/reactor.go` | `unmarshalDebtEventForReactor` (:107) |
| `recurringmovement/slices/executerecurringmovement/reactor.go` | `unmarshalRecurringEventForReactor` (:101) |

**One of them is already wrong.** `depositmoney`'s switch handles `WalletAdded`, `MoneyDeposited` and `WalletDisabled` but *not* `MoneyWithdrawn`, so it falls through to `return nil, nil`. `Apply(nil)` matches no case yet still increments `version`, so rehydration silently skips every withdrawal and `POST /wallets/:id/deposit` returns a balance inflated by the sum of all prior withdrawals. That is today's bug with zero versions in play; seven hand-maintained switches that each also have to know which upcaster chain to run is not a position to be in.

Error handling splits in two, and the distinction matters:

- **Unknown to the codec** (never registered anywhere) → hard error. This is the bug above.
- **Known to the codec but not handled by this projector or reactor** → skip silently. This is normal fan-out.

### 3.4 Upcasters are typed and chained

An upcaster is a plain Go function over a frozen historical struct, wired in through a generic adapter:

```go
// wallet/schema/v1.go — FROZEN, never edited again
type MoneyDepositedV1 struct {
    WalletID string `json:"wallet_id"`
    Amount   int    `json:"amount"`
}

// wallet/schema/upcast.go
codec.Add(eventstore.Typed("MoneyDeposited", 1,
    func(o MoneyDepositedV1) wallet.MoneyDeposited {
        return wallet.MoneyDeposited{
            WalletID: o.WalletID,
            Amount:   o.Amount,
            Category: "uncategorised", // a default the zero value cannot express
        }
    }))
```

Chained v1→v2→v3, so N upcasters rather than N². Frozen structs live in a `schema/` package per context, not in `domain/events.go`, so the ubiquitous language stays readable — a reader can tell at a glance which shapes are live.

Most changes need **no upcaster at all**: `json.Unmarshal` zeroes fields that are absent and ignores fields it does not know, so additive and subtractive changes are free. An upcaster is only needed for a rename, a type change, a default the zero value cannot express, a split or merge of fields, or a semantic reinterpretation. Those are rare and each one is a genuine domain translation, which is exactly where a mistyped snake_case key in a `map[string]any` would compile fine and silently mistranslate every replayed event. Typed upcasters put the compiler on that.

Upcasters are **1→1 by construction**. Splitting one event into two, merging two into one, or inventing information the old event never carried are not upcaster jobs — those get a new event type. Retiring a type means ceasing to *emit* it, never ceasing to *understand* it: its factory stays registered and its `Apply()` case stays in the aggregate forever.

### 3.5 Bump on every shape change

Including additive changes that need no upcaster. The version records *which shape was written*, not *which translations exist*; a chain step with no registered upcaster is identity.

The alternative — bumping only when a translation is required — keeps the chain dense but makes additive changes invisible. An event written before a field existed becomes indistinguishable from one where the user left it blank, so deciding later to backfill a different default is unrecoverable. Bumping always costs nothing at write time and keeps that door open.

The current version is declared once, at registration (`codec.Register("MoneyDeposited", 3, factory)`), and the codec stamps that number into metadata on append. Write and read agree by construction.

### 3.6 Golden fixtures enforce it

A policy that depends on remembering produces v2 data stamped `v1`, which is worse than no versioning because the chain will confidently mistranslate it.

```
wallet/schema/testdata/
  MoneyDeposited.v1.json   ← input to the v1→v2 upcaster test
  MoneyDeposited.v2.json   ← input to the v2→v3 upcaster test
  MoneyDeposited.v3.json   ← tripwire for the current shape
```

`TestSchemaFrozen` marshals a fully-populated sample of each current struct and diffs it against its fixture; editing a struct without bumping fails with a message saying to bump and add a fixture. The historical fixtures are simultaneously the exact inputs the upcaster tests need. One mechanism, two jobs. Cost is one `sample()` per event type.

---

## 4. Cross-aggregate processes

Full rationale in [ADR 0002](./adr/0002-cross-aggregate-processes.md).

`transfermoney/command_handler.go` currently writes three aggregates in one `AppendMulti` (:101) — source wallet, destination wallet, and the transaction — atomically via `TransactWriteItems`. KurrentDB cannot do that. Options considered were sequential-append-with-compensation, widening the aggregate boundary to one stream per user, and a saga. **Decision: saga, driven by the existing reactor.** It is the textbook event-sourced answer, it reuses machinery that already exists, and the learning value is the point of the exercise. The cost is that transfers become eventually consistent.

But the scope is wider than transfers, because two other flows already have the same shape — and both are broken today.

### 4.1 Two live bugs, not migration risks

Three flows cross an aggregate boundary. In two of them, the source aggregate **commits to a fact in the past tense and only then tries to move the money**:

```
RecurringMovementExecuted  appended  →  reactor withdraws  →  ErrInsufficientFunds
DebtSettled                appended  →  reactor withdraws  →  ErrInsufficientFunds
```

Both errors land in `isPermanent()` (`internal/reactor/router.go:145`) → `log.Warn` → **record dropped**. So:

- **A standing order silently skips a month.** `RecurringMovementExecuted` is written for that date, so the cron never retries it. The read model says it ran. No money left the wallet.
- **A debt reads settled while the money never left.** The user believes they paid something they didn't.

Neither needs a race, an outage, or a disabled wallet — only a month where the balance is short. These are happening on AWS right now.

The author of `settledebt/command_handler.go` knew:

```go
//A reactor withrawas money from the account, this just
//changes the state to "Settled"
```

The names are the tell. `RecurringMovementExecuted` is emitted before execution; `DebtSettled` is emitted before settlement. They are past-tense assertions about things that have not happened, and the reactor is what makes them true — except when it can't.

`Transaction` had the mirror-image problem: `Create()` and `Complete()` are called on consecutive lines (:71, :77) inside one `AppendMulti`, so `StatusCreated` has never been observed and `TransactionCreated` was never even registered. It was a receipt, not a process. Under a saga it becomes a state machine with observable intermediate states — including states where no money moved.

### 4.2 One uniform shape

Every cross-aggregate flow gets the same skeleton: **a request, zero or more steps, and exactly one terminal outcome.** The past tense is reserved for facts that are already true.

```
TRANSFER    transfer-<id>
  TransferRequested
    → TransferDebited
      → TransferCompleted | TransferFailed | TransferStuck

OCCURRENCE  occurrence-<rmID>-<yyyy-mm-dd>
  MovementDue
    → MovementExecuted | MovementSkipped{reason}

DEBT        debt-<id>
  DebtSettlementRequested
    → DebtSettled | DebtSettlementFailed{reason}
```

`Transaction` is retired from the vocabulary entirely. The HTTP route was already `POST /transfers`, and the word collided with "database transaction" — the precise mechanism this migration removes. The concept is **Transfer** in the glossary, the API, and the code.

`MovementSkipped` is user-facing. *"Your standing order was skipped this month — insufficient funds"* is what a bank tells you, and the read model and UI must be able to say it.

### 4.3 Transfer flow

```
POST /transfers
  ├─ validate command invariants (amount > 0, wallets differ, user present)
  ├─ load both wallets read-only for a balance precheck (fail fast, not a guarantee)
  ├─ append TransferRequested → transfer-<txID>            [one stream, atomic]
  └─ 202 {transfer_id, status: "pending"}

reactor:
  TransferRequested → withdrawmoney.Handle(idem: "<txID>:debit")
        ok   → append TransferDebited      → transfer-<txID>
        fail → append TransferFailed       → transfer-<txID>

  TransferDebited   → depositmoney.Handle(idem: "<txID>:credit")
        ok   → append TransferCompleted    → transfer-<txID>
        fail → wallet.Refund(idem: "<txID>:refund")   [compensate source]
                 ok   → append TransferFailed  → transfer-<txID>
                 fail → append TransferStuck   → transfer-<txID>, Nack(Park)
```

The precheck at the API boundary is **advisory only**, but it is not balance-only. It checks balance, currency and whether either wallet is disabled, so the statically-doomed cases fail fast with a 4xx instead of being accepted and then failing asynchronously.

It remains advisory because it cannot be anything else: everything it checks can change between the precheck and the reactor running. A transfer can still be accepted with 202 and fail, and `TransferFailed` carries the reason. The authoritative check is always the one inside `wallet.Withdraw` and `wallet.Deposit`.

**Revised during implementation.** This section originally specified a balance-only precheck, which would have let a EUR→USD transfer be accepted and debited before failing on the credit leg. Rejecting it up front is strictly better, and does not weaken the case for compensation — see §4.4.

### 4.4 Failure taxonomy

| Failure | Response |
|---|---|
| Business rejection **before** money moves | Terminal failure event. Nothing to compensate. |
| Business rejection **after** money moves | Compensate, then terminal failure event. |
| Compensation rejected by the domain | `wallet.Refund` exists so the common case cannot happen. If it still fails → `TransferStuck` + `Nack(Park)`. |
| Transient infrastructure failure | `Nack(Retry)`. |
| `WrongExpectedVersion` on the process stream | Reload the process; if the step is already recorded, ack. |
| No progress at all | Sweeper (§4.5). |

**`wallet.Refund` is a distinct aggregate method, not a `Deposit`.** Here is the scenario that forces it, every step of which is verifiable in the current code:

| | | |
|---|---|---|
| `t0` | Both wallets are EUR and healthy. `POST /transfers` €50 A→B | Precheck passes — everything it checks is fine *right now*. `TransferRequested`. 202. |
| `t1` | reactor debits A, idem `X:debit` | `MoneyWithdrawn` on `wallet-A`. **A is −€50.** `TransferDebited`. |
| `t2` | user disables **both** wallets | `WalletDisabled` ×2. Perfectly legal. |
| `t3` | reactor credits B, idem `X:credit` | `Deposit` → **`ErrWalletDisabled`**. Compensate. |
| `t4` | reactor refunds A, idem `X:refund` | `Deposit` → **`ErrWalletDisabled`** (`:118`). |
| `t5` | `isPermanent(ErrWalletDisabled)` → `true` | `router.go:148` — `log.Warn`, drop the record. |

**€50 gone.** The transfer reads `Debited` forever, no terminal event is ever written, and the only trace is one `Warn` line. The compensating action was subject to the same invariants as a normal deposit, and one of those invariants is under the user's control.

Note the failure is arranged as a **race**, not a static mismatch, precisely because §4.3's precheck now rejects the static case up front. No precheck can close this window — the state it validated is free to change while the reactor works — which is the whole reason compensation has to be correct rather than merely unlikely to be needed. `TestTheFiftyEuroScenario` drives it through `requesttransfer`, the same entry point the HTTP handler uses, so it proves the path against a transfer the real system would actually accept.

The domain resolution: *the disabled-wallet check on `Deposit` exists to stop **new** money entering a closed pot. Returning money you already took out of that same pot is not new money.* So `Refund` keeps the idempotency, positive-amount and currency checks and drops the disabled and category checks:

```go
func (a *Aggregate) Refund(userID string, amount int, currency, reason, idempotencyKey string, now time.Time) error {
    if idempotencyKey != "" && a.idempotencyKeys[idempotencyKey] { return ErrAlreadyProcessed }
    if amount <= 0                                              { return ErrInvalidAmount }
    if currency != a.currency                                   { return ErrCurrencyMismatch }
    // deliberately NO disabled check, NO category check — see ADR 0002
    a.raise(MoneyRefunded{...})
    return nil
}
```

`TransferStuck` is the backstop for compensation failures we have not anticipated, because they cannot all be enumerated in advance. This yields the invariant the whole design hangs on:

> **Failed ⇒ no money moved. Stuck ⇒ money is in limbo, and it is written down.**

Nothing is ever lost silently.

**Two kinds of idempotency, both required.** The wallet-level keys (`<txID>:debit`, `<txID>:credit`, `<txID>:refund`) stop money moving twice; they derive from the transfer ID and leg name rather than `meta.EventID`, so they stay stable even if the process stream is rebuilt or an event is re-emitted with a new envelope ID. Separately, a process-level state check stops the reactor livelocking on a conflict it caused itself: on `WrongExpectedVersion` it reloads the transfer, and if the step is already recorded it acks rather than retrying forever.

### 4.5 The sweeper

Everything above assumes the reactor runs. The failures it does not cover are the ones where **nothing fails**: the reactor is down for an hour, a message is parked before any handler executes, an unregistered event type hard-errors during decode (newly possible under §3.3), or an event is simply never routed. The process sits at `Requested` or `Debited` with no terminal event and no `TransferStuck`, because the reactor never got far enough to write one. Today nothing notices and the user sees a spinner forever.

A CronJob queries `processes_view` for non-terminal processes past a threshold and **re-drives them from their last recorded state**. Re-driving is safe precisely because every step carries a stable idempotency key, so re-running a step that already succeeded is a no-op. Timing out is *not* safe — a transfer at `Debited` has already moved money.

An attempt counter lives on the process stream so it survives cron restarts, and after K attempts the sweeper gives up. What it writes depends on whether money moved, which the stream tells it exactly:

```
state == Requested  → no money moved  → TransferFailed{reason: "timed out"}
state == Debited    → money is out    → TransferStuck
```

### 4.6 Stream identity

A Transfer is one stream, `transfer-<id>`. A debt settlement is one-shot and lives on `debt-<id>`. A Recurring Movement fires forever, so **its stream holds only the instruction and each firing gets its own stream**, `occurrence-<rmID>-<yyyy-mm-dd>`.

That split is not primarily about stream size. The stream name is derived from the recurring movement and the date, so appending `MovementDue` at `ExpectedRevision.NoStream` fails with `WrongExpectedVersion` if the date already fired:

> **The stream identity is the idempotency key.** No dedup table, no key to remember to pass.

It also keeps `recurringmovement-<id>` bounded (a daily standing order over five years would otherwise be ~3,600 events replayed by every command that touches the schedule), removes `expectedVersion` contention between occurrences catching up after an outage, and gives the sweeper a `$ce-occurrence` category to query.

### 4.7 Where processes live

```
internal/payments/
  wallet/  debt/  recurringmovement/     ← aggregate contexts
    domain/  slices/  projections/

  processes/                              ← the documented exception
    transfer/       domain/  reactor.go  slices/  projections/
    occurrence/
    debtsettlement/
```

This names an exception to the rule in `CLAUDE.md` that a slice owns everything it needs. A process manager's job is to issue commands against aggregates it does not own, so it is inherently cross-slice — `settledebt/reactor.go` already imports `wallet/slices/depositmoney` and `wallet/slices/withdrawmoney`. With three processes that becomes roughly nine deliberate edges. Left undocumented, someone eventually "fixes" it by introducing a command bus, which the same file also forbids.

**A process may import any slice. No slice may import a process.**

### 4.8 The double-movement regression

Commit `a3e4815` made `MovementsProjector` derive `transfer_out` + `transfer_in` movement rows from `TransactionCompleted`. Under the saga the wallet legs emit genuine `MoneyWithdrawn` / `MoneyDeposited` events, which that same projector already turns into movement rows. **Every transfer would appear twice.** `TransferCompleted` must be status-only and produce no movement rows. A projection test asserts that one completed transfer yields exactly two movement rows.

`MoneyRefunded` **does** produce a movement row — it is a genuine balance change, and omitting it would make the read-model balance diverge from the aggregate.

---

## 5. Event store port changes

### 5.1 Split the interface

`eventstore.Store` currently mixes a write port with an in-process pub/sub that only the memory store implements — `DynamoStore.Subscribe` and `DynamoStore.ReplayAll` are no-ops purely to satisfy it. Split:

```go
// Write side. What command handlers depend on.
type Store interface {
    Append(ctx context.Context, stream StreamID, events []Event, expectedVersion int) error
    Load(ctx context.Context, stream StreamID) ([]Event, error)
}

// Read side. What the projector and reactor depend on.
type Feed interface {
    SubscribeAll(ctx context.Context, from Position, handler func(Recorded) error) error
}
```

`AppendMulti` is deleted along with the atomic-transfer requirement. `RegisterEvent` moves to the **codec** (§3.3), which is the only thing that ever needed a type registry — and, contrary to the previous draft of this plan, that is *both* sides, not just the dispatcher. `Load` keeps returning raw `[]Event`; the codec decodes. The adapter stays dumb about domain types.

### 5.2 Stream identity

`Load(ctx, aggregateID)` gives the adapter no way to name a stream. Handlers already hard-code the aggregate type when building envelopes (`AggregateType: "wallet"`), so the information exists — it just isn't passed. Introduce `StreamID{Category, ID}` rendering as `wallet-<uuid>`, with per-aggregate helpers (`wallet.Stream(id)`, `transfer.Stream(id)`, `occurrence.Stream(rmID, date)`). Mechanical change across the command slices, and it buys `$ce-wallet` category streams for free.

### 5.3 Version and error mapping

| Domain concept | Today (DynamoDB) | KurrentDB |
|---|---|---|
| `expectedVersion` | 1-based count of prior events | `ExpectedRevision` (0-based); `NoStream` when count is 0 |
| Conflict | `ConditionalCheckFailedException` | `WrongExpectedVersion` |
| Missing aggregate | Empty query result | `StreamNotFound` |
| `EventID` | Item attribute | ESDB event ID — gives append-level dedup for free |
| `Version` | Sort key | Implicit stream revision; stop storing it |
| `correlation_id`, `occurred_at`, `aggregate_type`, **`schema_version`** | Item attributes | ESDB event *metadata* (separate from data) |

Both `ErrConcurrencyConflict` and `ErrAggregateNotFound` keep their current meaning; only the adapter-level mapping changes.

`expectedVersion` is taken from the **raw stream revision, never from `len(decoded)`**. `depositmoney/command_handler.go` currently passes `len(events)` from the raw slice, which happens to be correct but couples concurrency control to decode output.

### 5.4 Memory store becomes a mini-KurrentDB

Give `MemoryStore` stream semantics plus a global ordered log with positions, so it satisfies both `Store` and `Feed`. Every existing handler test keeps working, and all three processes can be built and debugged in phase 1 with no infrastructure running at all.

---

## 6. Read models

Two views change shape.

**`movements_view` stays a pure ledger.** Every row changes a balance, so its running total is always correct. `MoneyDeposited`, `MoneyWithdrawn`, `MoneyRefunded` — nothing else. A transfer that fails after the debit is therefore *already* visible here, as a withdrawal followed by a refund netting to zero; nothing needs injecting.

**`processes_view` is new** and holds what the ledger deliberately cannot: processes that moved no money.

```
kind        | id  | status    | reason              | requested_at | attempts
transfer    | ... | failed    | currency_mismatch   | ...          | 0
occurrence  | ... | skipped   | insufficient_funds  | ...          | 0
transfer    | ... | stuck     | refund_rejected     | ...          | 3
transfer    | ... | pending   |                     | ...          | 1
```

One view, two consumers: the UI reads it for status, and the sweeper (§4.5) queries it for non-terminal rows past the threshold. The rejected alternative was per-process views, which would make the sweeper union three tables and a fourth on the next process.

`txprojections` is currently written by its projector and read by nobody (`internal/app/projector.go`). It becomes `processes_view`, and a new `processes/transfer/slices/gettransfer` slice serves `GET /transfers/:id` so status is pollable.

**Checkpoints**: `008_create_projection_checkpoints.sql` with `(subscription_name, commit_position, prepare_position, updated_at)`, one row per projection. Projection stores change from `*pgxpool.Pool` to a small `DBTX` interface (`Exec`/`Query`/`QueryRow`) satisfied by both `*pgxpool.Pool` and `pgx.Tx`, so a projection's rows and its checkpoint commit together.

---

## 7. Code change inventory

Every file the migration touches, grouped by the decision that drives it. Phase numbers refer
to §8. "→" means the file moves; **DELETE** means it stops existing.

### 7.1 The codec and the schema seam (§3)

| Path | Change | Phase |
|---|---|---|
| `shared/eventstore/codec.go` | **NEW.** Type registry keyed by `(eventType, version)` + `Decode(Recorded)` / `DecodeAll([]Event)`. Hard-errors on a type unknown to the codec. Stamps `schema_version` into metadata on append. | 1 |
| `shared/eventstore/upcast.go` | **NEW.** `Upcaster` interface, the generic `Typed[From,To]` adapter, and the chain runner that treats a missing step as identity. | 1 |
| `shared/eventstore/codec_test.go` | **NEW.** Chain-with-gaps, unknown-type error, known-but-unhandled skip. | 1 |
| `payments/eventcatalog/` | **NEW package.** One `Entry` per event type — current version, factory, and a fully-populated sample — plus `testdata/<Type>.v<N>.json` and the frozen-shape tripwire. **Zero upcasters on day 1.** | 1 |
| `payments/<context>/schema/` | **Deferred until the first upcaster exists.** Originally specified as four packages created up front; with zero upcasters they would have been empty, and the per-context split costs the "single canonical list" property that §3.3 depends on. Frozen historical structs still belong here when there are any. | — |
| *(the seven decode switches)* | **DELETE.** Enumerated with line numbers in §3.3. | 1 |

### 7.2 Event store port (§5)

| Path | Change | Phase |
|---|---|---|
| `shared/eventstore/store.go` | Split `Store` (write) from `Feed` (read). Add `StreamID{Category,ID}`, `Position`, `Recorded`. **Drop** `AppendMulti`, `LoadAll`, `Subscribe`, `RegisterEvent`, `ReplayAll`, and the `Subscriber` interface — the codec and `Feed` replace them. | 1 |
| `shared/eventstore/memory.go` | Gains stream semantics and a global ordered log with positions; satisfies both `Store` and `Feed`. This is what all of phase 1 is built and tested against. | 1 |
| `shared/eventstore/errors.go` | `ErrConcurrencyConflict` / `ErrAggregateNotFound` keep their meaning; only adapter mapping changes (§5.3). | 1 |
| `shared/eventstore/kurrent.go` | **NEW.** `Store` + `Feed` against KurrentDB. `expectedVersion` → `ExpectedRevision`, `NoStream` at 0; `WrongExpectedVersion` → `ErrConcurrencyConflict`; `StreamNotFound` → `ErrAggregateNotFound`. | 2 |
| `shared/eventstore/contract_test.go` | **NEW.** One table run against memory and a real container, env-gated as `dynamodb_test.go` is today. Includes: appending `MovementDue` twice for one date must fail. | 2 |
| `shared/eventstore/dynamodb.go`, `dynamodb_test.go` | **DELETE** with the rest of the AWS surface. | 4.5 |

### 7.3 Wallet aggregate (§4.4)

| Path | Change | Phase |
|---|---|---|
| `wallet/domain/aggregate.go` | Add `Refund()`. Keeps the idempotency, positive-amount and currency checks; deliberately **no** disabled check and **no** category check. | 1 |
| `wallet/domain/events.go` | Add `MoneyRefunded`. | 1 |
| `wallet/domain/aggregate_test.go` | G/W/T: `Refund` succeeds on a **disabled** wallet; still rejects a currency mismatch; second call with the same key returns `ErrAlreadyProcessed`. | 1 |
| `wallet/slices/refundmoney/` | **NEW slice** — `command.go`, `command_handler.go`, `register.go`. **No HTTP handler and no route**: only a process ever issues a refund. | 1 |
| `wallet/services/transfer_money.go` | **DELETE.** A cross-aggregate domain service that withdraws and deposits in one call is exactly what a multi-stream transaction bought and KurrentDB removes; the two legs become separate reactor steps. It also hardcodes the `"Other"` / `"Transfer In"` categories and calls `time.Now()` internally, against the rule that `now` is passed in. | 1 |
| `wallet/slices/depositmoney/command_handler.go` | Decode via codec; `expectedVersion` from stream revision, not `len(events)`. Fixes the missing-`MoneyWithdrawn` bug (§3.3). | 1 |
| `wallet/slices/withdrawmoney/command_handler.go` | Decode via codec; same revision fix. | 1 |
| `wallet/slices/disablewallet/command_handler.go` | Decode via codec. Behaviour unchanged — disabling stays legal at any time, because `Refund` is what makes that safe. | 1 |
| `wallet/slices/addwallet/command_handler.go` | `StreamID` only; it never decoded anything. | 1 |
| `wallet/slices/{depositmoney,withdrawmoney}/projection.go` | **DELETE.** Comment-only stubs carrying a `TODO: Wire MoneyDeposited handling into the Wallets projector` that `wallets_projection.go` has since done. | 1 |

### 7.4 Transaction → Transfer (§4.2)

The whole `payments/transaction/` tree stops existing. `AggregateType: "transaction"` becomes the
`transfer` stream category. All of it is phase 1.

| From | To | Change |
|---|---|---|
| `transaction/domain/aggregate.go` | `processes/transfer/domain/aggregate.go` | States become `Requested` / `Debited` / `Completed` / `Failed` / `Stuck`. `Create`+`Complete` on consecutive lines becomes four separate transitions, each its own append. Add the attempt counter for the sweeper. |
| `transaction/domain/events.go` | `processes/transfer/domain/events.go` | `TransactionCreated` → `TransferRequested`; `TransactionCompleted` → `TransferCompleted`; **add** `TransferDebited`, `TransferFailed`, `TransferStuck`, `TransferRetryAttempted`. |
| `transaction/domain/errors.go` | `processes/transfer/domain/errors.go` | `ErrAlreadyCompleted` joined by the terminal-state guards. |
| `transaction/slices/transfermoney/` | `processes/transfer/slices/requesttransfer/` | Handler appends **one** event to one stream and returns **202** `{transfer_id, status:"pending"}`. The balance precheck stays but is documented as advisory. `AppendMulti` and both `walletEnvelopes` helpers go. |
| *(new)* | `processes/transfer/reactor.go` | **NEW.** The two saga steps plus compensation, per §4.3. |
| *(new)* | `processes/transfer/slices/gettransfer/` | **NEW slice.** `GET /transfers/:id`, so 202 is pollable. |
| `transaction/projections/` | `processes/transfer/projections/` | Writes `processes_view` instead of `transactions_view` — the table that was written by its projector and read by nobody. |
| `transaction/slices/transfermoney/errors.go` | `processes/transfer/slices/requesttransfer/errors.go` | `ErrSameWallet` survives as-is. |

### 7.5 Recurring movement → Occurrence (§4.1, §4.6)

| Path | Change | Phase |
|---|---|---|
| `recurringmovement/domain/events.go` | **Remove** `RecurringMovementExecuted` — it asserted execution before execution. `RecurringMovementRegistered` and `RecurringMovementStopped` stay; the instruction stream now holds only those two. | 1 |
| `recurringmovement/domain/aggregate.go` | Drops per-date execution state. It is now purely the schedule plus whether it is stopped. | 1 |
| `recurringmovement/slices/executerecurringmovement/` | **DELETE.** Both the command handler and the reactor move. | 1 |
| `processes/occurrence/domain/` | **NEW.** `MovementDue`, `MovementExecuted`, `MovementSkipped{reason}`, `MovementRetryAttempted`; stream `occurrence-<rmID>-<yyyy-mm-dd>`. | 1 |
| `processes/occurrence/reactor.go` | **NEW.** `MovementDue` → deposit or withdraw → `MovementExecuted` \| `MovementSkipped`. | 1 |
| `app/recurring_cron.go` | Appends `MovementDue` at `ExpectedRevision.NoStream`. A duplicate run fails with `WrongExpectedVersion`, which **is** the dedup — no idempotency key, no dedup table. | 1 |
| `app/recurring_cron_test.go` | Add: running the cron twice for one date produces exactly one `MovementDue`. | 1 |

### 7.6 Debt settlement (§4.1)

| Path | Change | Phase |
|---|---|---|
| `debt/domain/events.go` | **Add** `DebtSettlementRequested` and `DebtSettlementFailed{reason}`. `DebtSettled` survives but moves to the end of the flow, where it is finally true. | 1 |
| `debt/domain/aggregate.go` | `Settle()` raises `DebtSettlementRequested`; new `MarkSettled()` and `FailSettlement(reason)`. | 1 |
| `debt/slices/settledebt/command_handler.go` | Emits `DebtSettlementRequested`. **Delete the comment on line 60** — `//A reactor withrawas money from the account, this just changes the state to "Settled"` — which documented the bug rather than fixing it, and says "account" where the glossary says Wallet. | 1 |
| `debt/slices/settledebt/reactor.go` | → `processes/debtsettlement/reactor.go`. Now writes a terminal event on rejection instead of returning an error that gets dropped. | 1 |
| `debt/slices/settledebt/reactor_test.go` | → `processes/debtsettlement/reactor_test.go`. **Add** the case that currently has no coverage: insufficient funds must produce `DebtSettlementFailed`, not a silent drop. | 1 |

### 7.7 Dispatch, wiring and the sweeper (§2, §4.5)

| Path | Change | Phase |
|---|---|---|
| `app/store.go` | `registerDomainEvents` becomes codec registration, carrying the current version per type. Still the single canonical list. | 1 |
| `app/sweeper.go` | **NEW.** Queries `processes_view` for non-terminal rows past the threshold, re-drives from last recorded state, gives up state-aware after K attempts. | 1 |
| `app/sweeper_test.go` | **NEW.** Advance a clock against the memory store: stalled at `Requested` → `TransferFailed`; stalled at `Debited` → `TransferStuck`; a step that already succeeded is a no-op. | 1 |
| `projector/dispatcher.go` | Takes the codec. `HandleBatch([]KinesisEventRecord)` → `Handle(Recorded)`. | 3 |
| `projector/kinesisdecode/decode.go` | **DELETE.** | 3 |
| `reactor/router.go` | Rewrite. `classify()` returns `Ack` / `Nack(Park)` / `Nack(Retry)`. **The behaviour change of §4.1 lives here**: a permanent error now writes a terminal process event *before* acking, instead of `log.Warn` + drop. Also gains the `WrongExpectedVersion` → reload-and-check path. | 3 |
| `app/projector.go` | Five independent catch-up subscriptions, each committing its rows and its own checkpoint in one transaction. | 3 |
| `app/reactor.go` | Persistent subscription on `$all`. | 3 |
| `app/devdriver.go` | **DELETE.** The poll-the-events-table pump existed only to fake Kinesis locally; real subscriptions make local and deployed topology identical. | 3 |
| `app/routes.go` | Rewire for `processes/`, the new `gettransfer` slice, and the 202 transfer response. | 1 |

### 7.8 Read models and migrations (§6)

| Path | Change | Phase |
|---|---|---|
| `008_create_projection_checkpoints.sql` | **NEW.** `(subscription_name, commit_position, prepare_position, updated_at)`, one row per projection. (`005` was never used; `008` is the next free number.) | 3 |
| `009_create_processes_view.sql` | **NEW.** `processes_view` — `kind`, `id`, `status`, `reason`, `requested_at`, `attempts`. Drops `transactions_view`. | 3 |
| `wallet/projections/movements_projection.go` | Handle `MoneyRefunded`. **Stop deriving `transfer_out`/`transfer_in` rows from the transfer completion event** — the wallet legs now emit real movements, so leaving both in place double-counts every transfer (§4.8). | 1 |
| `wallet/projections/movements_projection_test.go` | **NEW/extend.** One completed transfer yields exactly two `movements` rows. One failed-after-debit transfer yields exactly two that net to zero. | 1 |
| `*/projections/*_postgres_store.go` | `*pgxpool.Pool` → a `DBTX` interface (`Exec`/`Query`/`QueryRow`) satisfied by both `*pgxpool.Pool` and `pgx.Tx`, so rows and checkpoint commit together. | 3 |
| `transaction/projections/*` | → `processes/transfer/projections/*`, writing `processes_view`. | 1 |

### 7.9 Binaries

| Path | Change | Phase |
|---|---|---|
| `cmd/sweeper/main.go` | **NEW.** Run-once-and-exit. | 3 |
| `cmd/projector/main.go`, `cmd/reactor/main.go` | Long-running loops with graceful shutdown. Drop `aws-lambda-go`. | 3 |
| `cmd/recurring-cron/main.go`, `cmd/migrate/main.go` | Run-once-and-exit. Drop `aws-lambda-go`. | 3 |
| `cmd/lambda/main.go` | **DELETE** with the Makefile Lambda targets and the AWS SDK deps outside `internal/voice`. | 4.5 |
| `cmd/server/main.go`, `cmd/devtools/main.go` | Unchanged in shape; `devtools` gains `replay <projection>` in phase 6. | — |

### 7.10 Untouched

`internal/voice/` (until phase 4.5 decides Transcribe's fate) · `shared/middleware/` (until phase 5)
· `shared/database/database.go` · every `handler.go` and `register.go` in the query slices
(`getuserwallets`, `getwalletmovements`, `getusermovements`, `getdebts`, `getrecurringmovements`)
· `addwallet`, `recorddebt`, `recordrecurringmovement`, `stoprecurringmovement` beyond the codec
and `StreamID` mechanics.

---

## 8. Phases

### Phase 1 — Domain and ports (pure Go, no new infrastructure)

By far the longest phase, and deliberately so: every decision from §3 and §4 is proven against the memory store before a container is pulled. AWS keeps running the old code throughout.

1. **Codec + upcaster seam** (§3). Registry with zero upcasters, `Typed[From,To]` adapter, hard-error on unknown types. Delete all seven decode switches — this fixes the `depositmoney` missing-`MoneyWithdrawn` bug as a side effect.
2. **Golden fixtures** for all current event types, plus `TestSchemaFrozen`.
3. **`Store` / `Feed` split, `StreamID`, memory store gains streams + a global ordered log.**
4. **`wallet.Refund` + `MoneyRefunded`**, with Given/When/Then tests asserting it succeeds on a disabled wallet and still rejects a currency mismatch.
5. **All three processes** under `internal/payments/processes/`, with the renames. `transaction` → `transfer`; `RecurringMovementExecuted` → `MovementDue` / `MovementExecuted` / `MovementSkipped`; `DebtSettled` → `DebtSettlementRequested` / `DebtSettled` / `DebtSettlementFailed`. Per-occurrence streams.
6. **The sweeper logic**, unit-tested against the memory store by advancing a clock.
7. **`processes_view`**, and the double-movement regression test from §4.8.

**Done when**: `go test ./...` is green, `make run` boots in memory mode, a transfer completes end-to-end through the in-process feed, a transfer whose credit leg is refused lands on `TransferFailed` with the source wallet whole, and a recurring withdrawal against an insufficient balance produces `MovementSkipped` rather than a silent no-op.

#### What landed early, and why

Three items tabled for later phases came forward, because the port split left them with no working form:

- **The Kinesis decoders** (`internal/projector/kinesisdecode`, the `HandleBatch` entry points, `reactor/router.go`'s `classify`) existed only to translate DynamoDB-Streams records. Once `Store`/`Feed` replaced the old interface they had no input format, so they are gone rather than maintained as a bridge to something being deleted. The permanent-vs-transient *classification* they carried was right and survives — it moved into `processes/rejection`, where each process records a terminal outcome instead of dropping the record.
- **`devdriver.go`** polled the DynamoDB table on a ticker and wrapped rows in synthetic Kinesis records. With a real global log behind `Feed` that fake is unnecessary; `app/async.go` subscribes for real, so local and deployed topology now match.
- **`009_create_processes_view.sql` and the shared contract table** came with the code that needs them. `008_create_projection_checkpoints.sql` did **not** — checkpoints are still phase 3, and until then every subscription starts from the beginning of the log, which is correct but not fast.

`cmd/projector` and `cmd/reactor` are now the long-running subscribers phase 3 specifies. They refuse to start against a store with no global log, which today means they refuse to start at all — DynamoDB has no `$all`. They come alive with the KurrentDB adapter. `make dev-stack` is parked for the same reason; `make run` is the full local stack in the meantime.

### Phase 2 — KurrentDB adapter — **DONE**

`internal/shared/eventstore/kurrent.go` implements `Store` + `Feed`, with `schema_version`, `correlation_id`, `occurred_at` and `aggregate_type` in KurrentDB's user metadata, separate from the event data. `docker-compose.yml` gained a `kurrentdb` service and lost `localstack`.

**Done when**: the same contract table passes against both stores. It does — all 13 cases, unchanged. Beyond that, the saga itself now runs on the real database: `TestTheSagaRunsOnKurrentDB`, `TestCompensationRunsOnKurrentDB`, and `TestTheStreamNameIsTheIdempotencyKeyOnKurrentDB`, which checks ADR 0002's dedup claim against the engine that actually enforces it. `make test-kurrent` runs them; `make dev-stack` now runs the whole application on KurrentDB + Postgres.

#### The `$all` subscription must be filtered — this settles §10's open question

The first run against KurrentDB **stopped the entire read side**, and the reason is worth recording because it is not obvious.

The codec hard-errors on an unknown event type, deliberately: for a stream we own, a type nobody registered means the registry and the log disagree, and continuing would silently skip history ([ADR 0001](./adr/0001-event-schema-versioning-and-upcasting.md)). Applied to `$all` the same rule is an availability cliff. `$all` is the *whole database* — KurrentDB's own system streams, anything another application writes, anything left from a previous life of the data. One foreign event, once, and the projector and every process stop forever.

`SubscribeAll` therefore filters server-side to the stream categories this application owns (`streams.All()`). Inside those categories an unknown type really is a fault and still halts; outside them we never look.

So: **`$all` with a filter, not a subscription per category.** Global ordering is preserved, which `MovementsProjector` and the sweeper both depend on. The one trap is that adding a category to `payments/streams` without adding it to `All()` means its events are never projected — which is why `All()` sits directly beneath the category constants.

### Phase 3 — Replace the Kinesis pipeline — **DONE**

- `projector.Dispatcher` keeps its subscriber fan-out but takes the codec; `HandleBatch([]KinesisEventRecord)` becomes `Handle(Recorded)`.
- Five catch-up subscriptions, one per projection, each committing rows + checkpoint in one transaction (§6).
- Delete `internal/projector/kinesisdecode` and `internal/app/devdriver.go`. The dev-only poll pump exists solely to fake Kinesis on a laptop; real subscriptions make local and deployed topology identical, which is a genuine win.
- Reactor moves to a persistent subscription; `classify()` returns `Ack` / `Nack(Park)` / `Nack(Retry)` instead of `nil` / `error`. Permanent errors now write a terminal process event **before** acking — the behaviour change §4.1 is about.
- Rewrite the `main.go`s: long-running loops with graceful shutdown for projector and reactor, run-once-and-exit for `recurring-cron`, `sweeper` and `migrate`. Drop `aws-lambda-go` from all of them.

**Done when**: `docker compose up` runs the whole stack and local topology matches §2 exactly. It does — `make dev-stack` boots KurrentDB + Postgres + the API with five independently-checkpointed projections and the reactor on a persistent subscription, verified by a transfer completing end to end and the subscription group reading `Live` in KurrentDB.

#### What implementing it turned up

**An in-process cache cannot coexist with a checkpoint.** Three projections kept one to avoid a round trip. A projection resuming from a checkpoint starts with an empty cache and a populated table, so every entity created before the checkpoint misses — the caches only ever worked because the old pipeline always replayed the whole log. They are gone; projections are stateless and read through their store, which is also what makes them correct with more than one replica.

**`Subscriber.Handle` had to start returning an error.** Without it a projection that failed to write would still advance its checkpoint, leaving a permanent hole that nothing would notice. Now the rows and the position share a transaction and a failure rolls back both.

**Two bugs surfaced that the phase-1 reviews missed.** `wallets_projection` never handled `MoneyRefunded`, so after a compensated transfer the projected balance sat below the aggregate forever — and the projected number is the one a person sees. And migrations `008`/`009` were missing their `-- +migrate Up` annotations, so `009` had been dead since phase 1; nothing had run migrations, so nothing had noticed.

**`MemoryCheckpoints` needed a mutex.** Five runners share one. Every single-runner test passed and `go test -race` never saw it, because the race only exists when runners actually run at the same time.

#### Retry, park, or done

The reactor's three outcomes are the point of the persistent subscription, and they replace a two-outcome scheme where a permanent rejection took the same path as success:

| | |
|---|---|
| **Done** | Handled — including a rule-based rejection, because the process has already recorded its terminal outcome. The work is done; the answer was just "no". |
| **Retry** | A transient fault. Redelivery is free: every step carries an idempotency key. The classifier biases here, because retrying something that would have succeeded costs a redelivery while parking it costs a person noticing. |
| **Park** | It cannot improve. An unregistered event type will not become registered by being redelivered, and retrying forever would block every process behind it. Parking is logged at ERROR — a parked message nobody looks at is the silent drop again, in a different queue. |

### Phase 4 — Containers, kind, Helm

Manifests live in a **new sibling repo, `money-management-k8s`**, mirroring how `money-management-infra` owns Terraform today — and giving ArgoCD something to watch if phase 6 goes that way.

```
money-management-k8s/
  chart/
    Chart.yaml   values.yaml   values-local.yaml
    templates/
      kurrentdb-statefulset.yaml   postgres-statefulset.yaml
      api-deployment.yaml          projector-deployment.yaml
      reactor-deployment.yaml      recurring-cron-cronjob.yaml
      sweeper-cronjob.yaml         web-deployment.yaml
      migrate-job.yaml
      ingress.yaml  configmap.yaml  secret.yaml
  kind-cluster.yaml
  Makefile     # kind-up, build, load, install, logs, nuke
  README.md
```

Decisions inside this phase:

- **One backend image, five binaries**, selected per workload by `command:`. Fewer builds, one tag to reason about.
- **Hand-written StatefulSets for Postgres and KurrentDB**, not pulled subcharts. More YAML, but PVCs, headless services and volume claim templates are half the reason for doing this.
- **`migrate` as a `pre-install,pre-upgrade` Helm hook Job.**
- **Projector at `replicas: 1` with `strategy: Recreate`.** Independent checkpoints do not make a projection safe to run twice concurrently.
- **Web image needs runtime config**: `VITE_API_URL` is baked at build time today. An `envsubst` entrypoint generating `/config.js` keeps one image usable across environments.
- **Probes**: API readiness checks both backends; the projector exposes **per-subscription** checkpoint lag (`$all` head position − stored checkpoint), labelled by subscription name, so liveness means something. The liveness signal is the *oldest* checkpoint.

**Done when**: `helm install` boots the entire app on a fresh kind cluster and the seeded data round-trips.

### Phase 4.5 — Decommission AWS

1. `aws dynamodb scan --table-name mm-events-dev > events-backup.jsonl`, committed somewhere durable. **Do this before anything else** — destroy is irreversible, and it keeps the door open for writing a DDB→KurrentDB importer later as an exercise.
2. `terraform destroy` in `envs/dev`.
3. Archive `money-management-infra`.
4. Delete `cmd/lambda`, the Lambda build targets in the Makefile, and the AWS SDK dependencies that remain outside the `voice` slice.

Note: the `voice` slice depends on Bedrock and Transcribe. It already falls back to the direct Anthropic API when `ANTHROPIC_API_KEY` is set, so it survives teardown in local-dev mode — but Transcribe streaming has no such fallback and will stop working. Decide then whether to drop the audio endpoint or keep an AWS credential around just for it.

### Phase 5 — Auth and frontend

Phases 1–4 run on the existing `X-Dev-User` header; the SPA's transfer page stays feature-flagged off. Then:

- Replace the API-GW `ClaimsSource` with a JWKS-verifying implementation configured by `OIDC_ISSUER` / `OIDC_AUDIENCE`, so the provider is a config choice rather than a code change.
- Pick the provider — Cognito (keeps existing users, requires outbound internet) or Keycloak in-cluster (self-contained, more k8s learning, needs an Amplify → `oidc-client-ts` swap).
- Rebuild the transfer UI against the 202 + poll flow, and surface `MovementSkipped` and `TransferStuck` — the failure states are the point, and a UI that can only render success would hide exactly what §4 exists to make visible.

### Phase 6 — Operations (optional, pick by appetite)

Prometheus + Grafana with per-subscription projection-lag metrics · ArgoCD for GitOps · CloudNativePG operator replacing the hand-rolled Postgres StatefulSet · NetworkPolicies, resource limits, PodDisruptionBudgets · replay tooling (`devtools replay <projection>` = truncate one projection, delete one checkpoint row, restart — which §6's independent checkpoints make a per-projection operation, and which is far cleaner than anything possible on Kinesis).

---

## 9. Deferred / explicitly not doing

- **Multi-node KurrentDB.** Single node, `--insecure`, one PVC. Clustering is a separate exercise.
- **TLS.** Plain HTTP through the Ingress locally.
- **Deploying to a real cluster.** Local kind only. Nothing in the chart should assume otherwise, but no cloud provider work is in scope.
- **Migrating existing event data.** Fresh start; the JSONL dump is insurance, not a plan. This is why §3 ships an *empty* upcaster registry — there is no history to upcast.
- **1→N upcasting.** There are zero upcasters today, so widening `func(From) To` to `func(From) []To` later is a refactor of nothing.
- **Cross-user transfers.** The saga supports them, but the domain still assumes one `cmd.UserID` per transfer.
- **Snapshots.** Per-occurrence streams keep the one stream that would have grown unboundedly small enough not to need them.

---

## 10. Open questions

- **Is `MoneyRefunded` its own event type, or `MoneyDeposited` with a flag?** §4.4 and `CONTEXT.md` assume the former. Cheap to settle now, expensive after the first event is written.
- **Failure `reason` vocabulary.** Reuse the existing snake_case `error_code` strings (`insufficient_funds`, `currency_mismatch`), or a separate closed set? These land in `processes_view` and then in the UI.
- **Sweeper threshold and K.** No number is obviously right; easy to tune later.
- **Erasure.** `Reason`, `Name` and `Description` are free-text personal data in an append-only store. Nothing here addresses it.
- Does the `voice` slice keep its Transcribe endpoint after teardown, or drop to text-only?
- Should the reactor run >1 replica once persistent subscriptions are in? Correctness allows it; there's no load reason to, but it's a good way to *prove* the idempotency keys work.
- A shared dev KurrentDB accumulates streams from old test runs, and `make dev-reset` only wipes Postgres. Either teach it to wipe KurrentDB too, or settle on `docker compose down -v` as the reset.

### Answered

- ~~Is `$all` with a server-side exclusion filter the right subscription, or should the projector subscribe per category?~~ **Phase 2**: `$all`, filtered server-side by stream category. It turned out to be a correctness boundary rather than an optimisation — see §8's phase 2 notes.
