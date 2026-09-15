# Money Management — Backend

Go API server for personal money management. Event Sourcing + DDD + Vertical Slicing.

## Tech

- Go (latest stable)
- KurrentDB (event store, phase 2) / in-memory (phase 1) — PostgreSQL for projections
- REST API with JSON (snake_case fields always)
- All money amounts are integer cents — never use float for money

## Architecture: Vertical Slicing

Each feature is a self-contained slice. A slice owns everything it needs — no shared `services/`, `utils/`, or `helpers/` across slices.

### Aggregates vs Slices vs Processes

**Aggregates** hold domain state, events, and invariants. They live in the bounded context's `domain/` package (e.g., `payments/wallet/domain/`, package name `wallet`).

**Slices** are organized per command/use case (e.g., `addwallet/`, `depositmoney/`) and live under the context's `slices/` folder. Each slice owns its command, handler, errors, and registration. Slices import the aggregate they operate on.

**Processes** live in `payments/processes/` and carry work that spans more than one aggregate. They are the documented exception to "a slice owns everything it needs": a process manager's whole job is issuing commands against aggregates it does not own.

**A process may import any slice. No slice may import a process.** The arrow points one way. If you find yourself wanting a command bus to avoid the import, don't — the import is the design. See [ADR 0002](./docs/adr/0002-cross-aggregate-processes.md).

### Folder structure

```
backend/
  internal/payments/
    wallet/                     # bounded context
      domain/                   # aggregate package (package wallet)
        aggregate.go            # aggregate root + Apply()
        aggregate_test.go       # Given/When/Then table-driven tests
        errors.go               # sentinel domain errors
        events.go               # domain events (WalletAdded, MoneyDeposited, etc.)
      projections/              # read models + their stores (memory + postgres)
        wallets_projection.go
        wallets_memory_store.go
        wallets_postgres_store.go
      slices/
        addwallet/              # slice (command/use case)
          command.go            # command definition
          command_handler.go    # command handling + orchestration
          errors.go             # sentinel domain errors for this use case
          handler.go            # HTTP handler (REST endpoint)
          handler_test.go       # HTTP contract tests (in-memory event store)
          register.go           # RegisterCommands()
        depositmoney/
          ...same structure...
        withdrawmoney/
          ...same structure...
        refundmoney/            # no handler.go, no route — processes only
      schema/                   # frozen historical event shapes + upcasters
                                # (created when the first upcaster exists)
    debt/  recurringmovement/   # same shape

    processes/                  # cross-aggregate processes (the import exception)
      transfer/
        domain/                 # process aggregate + its events
        reactor.go              # the saga steps + compensation
        slices/                 # requesttransfer (202), gettransfer
      occurrence/               # one firing of a recurring movement
      debtsettlement/
      projections/              # processes_view — ONE view for all three kinds,
                                # because the sweeper queries one table
      sweeper/                  # re-drives processes that stall
      rejection/                # maps aggregate refusals to reason codes

    eventcatalog/               # THE list of event types + versions
      testdata/                 # golden fixture per (type, version)
    streams/                    # every stream name in one place
  specs/
    wallet.md                   # event model spec (markdown, normalized to YAML by skill)
    transfer.md
  pkg/
    logger/
      logger.go               # Logger interface + slog implementation
  shared/
    eventstore/
      store.go                # Store (write) + Feed (read) + StreamID + Recorded
      codec.go                # type registry + upcaster chains — the ONLY decoder
      upcast.go               # Typed[From,To] adapter
      errors.go               # ErrConcurrencyConflict, ErrAggregateNotFound
      memory.go               # streams + global ordered log; satisfies both ports
      kurrent.go              # KurrentDB implementation (phase 2)
    middleware/
      correlation.go          # generates UUID correlation_id, injects into context
      logging.go              # logs method, path, status, duration via logger interface
  cmd/
    server/
      main.go                 # Gin server + middleware + Register calls per slice
```

### When shared code is truly needed

Put it in `shared/` but this should be rare. Valid reasons: event store interface, middleware, money value object. Invalid reasons: "it might be reused someday."

## Wiring Pattern

Each slice exports a registration function:

- `RegisterCommands(router *gin.Engine, store eventstore.Store, codec *eventstore.Codec)` — wires HTTP handler + command handler
- `RegisterQueries(router *gin.Engine, ...)` — wires read-side handlers

A slice with no HTTP surface has no `RegisterCommands` at all. `refundmoney` is the example: a refund is only ever issued by a process compensating for a failed step, never by a person, and exposing it would let a caller mint money past the checks `depositmoney` enforces.

`main.go` calls one `RegisterCommands` line per slice. No DI frameworks, no global registries, no `func init()` magic.

## Event Sourcing Rules

- Events are the source of truth — state is derived by replaying them
- Never mutate aggregate state directly; always apply through events
- Events are immutable facts named in past tense (e.g., `MoneyDeposited`, `WalletAdded`)
- The `Apply(event)` method on aggregates is a pure state transition — no side effects, no validation. Used for rehydration (replaying from store). Does NOT track changes.
- Projections subscribe to events and build read-optimized views
- Event store is append-only

### Event schema versioning (CRITICAL)

Full rationale in [ADR 0001](./docs/adr/0001-event-schema-versioning-and-upcasting.md).

- **`payments/eventcatalog` is the single list.** Adding an event type means adding one `Entry`. Changing a shape means bumping its `Version` and regenerating its fixture.
- **The version lives in metadata**, never in the event type name. Type names are stable forever, so routing tables never change on a bump.
- **Bump on every shape change**, additive ones included. The version records which shape was written, not which translations exist — that is what keeps "backfill a different default for old events" possible later.
- **Most changes need no upcaster.** `json.Unmarshal` zeroes absent fields and ignores unknown ones. You only need one for a rename, a type change, a default the zero value cannot express, a split/merge, or a semantic reinterpretation.
- **Upcasters are typed and 1→1.** Freeze the old struct in the context's `schema/` package, write a plain Go func, wire it with `eventstore.Typed`. Splits, merges, and changes needing absent information get a **new event type** instead.
- **Retiring a type means ceasing to emit it, never ceasing to understand it.** Its factory stays registered and its `Apply()` case stays forever.
- Regenerate fixtures with `go test ./internal/payments/eventcatalog -update`.

### Decoding

**`eventstore.Codec` is the only place a payload is decoded.** Never write a
private `unmarshalEvent` switch — there were seven of them once, and one had
silently been missing a case long enough to corrupt every deposit response.

- Unknown to the codec → hard error. A registered type a given consumer does not handle → skip. These are different, and only the first is a fault.
- `expectedVersion` comes from the stream revision (`eventstore.ExpectedRevisionAfter`), never from the count of decoded events.

### Projection Idempotency (CRITICAL)

Projections replay from the beginning of the log on boot. Checkpoints ([ADR 0003](./docs/adr/0003-independent-projection-subscriptions.md)) make that fast; they are not what makes it safe. **Every projection must be idempotent.**

- **Never use `uuid.New()` for projection row IDs.** Use deterministic IDs derived from event data: `meta.EventID`, or a stable domain field.
- When one event produces multiple rows, use `meta.EventID + "-suffix"` to keep IDs unique and stable.
- Use `ON CONFLICT (id) DO NOTHING` for insert-only projections or `ON CONFLICT (id) DO UPDATE` for mutable projections.
- **`movements_view` is a pure ledger**: every row changes a balance. Processes that moved no money belong in `processes_view`. Do not re-derive movement rows from process events — the wallet legs already emit real ones, and doing both shows every transfer twice.

### Aggregate Event Ownership (CRITICAL)

- **Aggregates own event creation, never handlers.** Command handlers NEVER create domain events directly.
- Aggregates hold an internal `changes []DomainEvent` slice for uncommitted events
- `raise(event)` is a private method: calls `Apply()` (state transition) + appends to `changes`
- `Changes()` returns uncommitted events for the handler to wrap in envelopes and persist
- For creation: use a package-level factory function (e.g., `wallet.Create(...)`) that validates invariants, calls `raise()`, and returns `(*Aggregate, error)`
- For mutations on existing aggregates: use methods (e.g., `agg.Deposit(...)`) that validate invariants, call `raise()`, and return `error`
- **All business rule validation lives in the aggregate**, never in command handlers
- Domain errors (sentinel vars) live in the aggregate package (e.g., `wallet/errors.go`), not in slice packages
- Command handlers are pure orchestration: call aggregate → get changes → wrap in envelopes → store
- Pass `walletID` and `now` as parameters to aggregate methods (not generated internally) for deterministic testing

### Event Store

Two ports, in `shared/eventstore/store.go`:

- **`Store`** (write side): `Append(ctx, stream StreamID, events, expectedVersion)`, `Load(ctx, stream) ([]Recorded, error)`
- **`Feed`** (read side): `SubscribeAll(ctx, from Position, handler)`

- Stream names come from `payments/streams`, never built inline. A `StreamID` renders as `wallet-<uuid>`, which is what KurrentDB's `$ce-` category projections key off.
- Revisions are **0-based**; `NoStream` (-1) asserts a stream does not exist yet.
- Appending at `NoStream` to a deterministically-named stream is how occurrences dedupe: **the stream name is the idempotency key**, so there is no dedup table.
- There is no `AppendMulti`. Nothing writes two aggregates at once — that is what processes are for.

### Cross-aggregate processes

Every flow touching more than one aggregate takes the same shape: **a request, zero or more steps, and exactly one terminal outcome.**

- **Past tense only once true.** `MovementDue` → `MovementExecuted` | `MovementSkipped`, not `RecurringMovementExecuted` before anything has been paid.
- **Two kinds of idempotency, both needed.** Wallet-level keys (`<txID>:debit`) stop money moving twice; a process-level state check stops the reactor livelocking on a conflict it caused itself.
- **Failed ⇒ no money moved. Stuck ⇒ money is in limbo, and it is written down.** Never drop a permanent rejection — record a terminal event.
- A rejection the aggregate raised is permanent (record the outcome); anything unrecognised is transient (retry). `payments/processes/rejection` owns that split.

## DDD Rules

- Aggregates protect invariants (e.g., "balance cannot go negative") — all validation in aggregate methods, zero in handlers
- One invariant is deliberately weaker than its sibling: `wallet.Refund` skips the disabled-wallet and category checks that `Deposit` enforces, because returning money already taken out of a wallet is not new money arriving. Weakening it was the fix for funds being stranded when a wallet was disabled mid-transfer.
- Value objects are immutable (e.g., Money, WalletID, Currency)
- Domain logic lives in aggregates and value objects, never in handlers or infrastructure
- Use ubiquitous language everywhere: wallet, deposit, withdrawal, transfer, balance
- Commands represent intent; events represent facts
- Domain events implement `DomainEvent` interface (`EventType() string`)

## API Conventions

- RESTful endpoints per slice (e.g., `POST /wallets`, `POST /wallets/:id/deposit`)
- All JSON fields use snake_case
- HTTP handlers are thin — parse request, call command handler, return response
- Use proper HTTP status codes (201 for creation, 400 for domain violations, 404 for missing aggregates)

### Error responses

Consistent shape across all slices:

```json
{"error_code": "insufficient_funds", "message": "cannot withdraw 2000: balance is 1500"}
```

- `error_code`: machine-readable, snake_case
- `message`: human-readable
- Domain errors (ErrInsufficientFunds, etc.) → 400/404/409 via `errors.Is()` in handler
- Infrastructure errors → 500 with generic message, logged with slog.Error

## Observability

- **Logging**: Go stdlib `slog` — no external logging libraries
- **Correlation ID**: UUID generated server-side per request via middleware, injected into context. Flows through command → events (stored with correlation_id) → projections
- **HTTP middleware**: Correlation ID injection + request/response logging (method, path, status, duration). Applied once at router level, not per-slice
- **Log fields**: Every log line carries correlation_id, slice, aggregate_id, action
- Never add request logging inside slice handlers — middleware handles it

## Testing

Three layers, in order of importance:

1. **Aggregate tests** (`wallet/aggregate_test.go`): Given/When/Then with table-driven tests. Given past events, when command, then new events or error. Pure, no infrastructure. 80% of test effort here.
2. **Handler tests** (`createwallet/handler_test.go`): HTTP request → verify status + JSON shape. Uses in-memory event store. Tests the full vertical path per slice.
3. **Process tests** (`processes/transfer/reactor_test.go`): drive a reactor over a memory store and assert the money. The failure paths are the point — rejection before the debit, compensation after it, redelivery as a no-op.
4. **Projection tests**: Given events, verify read model. Only when the projection has logic beyond simple mapping — or when it guards a known regression, like one transfer yielding exactly two movement rows.

No mocking — Event Sourcing makes it unnecessary. Aggregates are pure functions over events, in-memory event store replaces mocks.

## Code Style

- Standard Go formatting (`gofmt`)
- No interface until you have two implementations (except EventStore — that's the boundary)
- Error handling: wrap with context using `fmt.Errorf("doing X: %w", err)`
- No panics in production code

## Event Modeling Workflow

Miro (visual event model) → `specs/<slice>.md` (markdown) → skill normalizes to YAML → scaffold skill generates slice code. Always write the spec before writing code.

## Commands

```bash
go test ./...          # run all tests
go test ./internal/payments/eventcatalog -update   # regenerate event fixtures after a version bump
go vet ./...           # vet code
go build ./...         # verify compilation
golangci-lint run      # lint (if installed)
go run ./cmd/server    # start the server
```
