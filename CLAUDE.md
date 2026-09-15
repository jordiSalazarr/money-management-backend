# Money Management — Backend

Go API server for personal money management. Event Sourcing + DDD + Vertical Slicing.

## Tech

- Go (latest stable)
- PostgreSQL (event store + projections)
- REST API with JSON (snake_case fields always)
- All money amounts are integer cents — never use float for money

## Architecture: Vertical Slicing

Each feature is a self-contained slice. A slice owns everything it needs — no shared `services/`, `utils/`, or `helpers/` across slices.

### Aggregates vs Slices

**Aggregates** hold domain state, events, and invariants. They live in the bounded context's `domain/` package (e.g., `payments/wallet/domain/`, package name `wallet`).

**Slices** are organized per command/use case (e.g., `addwallet/`, `depositmoney/`) and live under the context's `slices/` folder. Each slice owns its command, handler, errors, and registration. Slices import the aggregate they operate on.

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
  specs/
    wallet.md                 # event model spec (markdown, normalized to YAML by skill)
    transfer.md
  pkg/
    logger/
      logger.go               # Logger interface + slog implementation
  shared/
    eventstore/
      store.go                # EventStore interface + event envelope
      errors.go               # ErrConcurrencyConflict, ErrAggregateNotFound
      memory.go               # in-memory implementation (tests + early dev)
      postgres.go             # PostgreSQL implementation (later)
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

- `RegisterCommands(router *gin.Engine, store eventstore.Store, log logger.Logger)` — wires HTTP handler + command handler
- `RegisterProjections(db, eventSubscriber)` — wires projection updaters (added per slice when needed)

`main.go` calls one `RegisterCommands` line per slice. No DI frameworks, no global registries, no `func init()` magic.

## Event Sourcing Rules

- Events are the source of truth — state is derived by replaying them
- Never mutate aggregate state directly; always apply through events
- Events are immutable facts named in past tense (e.g., `MoneyDeposited`, `WalletAdded`)
- The `Apply(event)` method on aggregates is a pure state transition — no side effects, no validation. Used for rehydration (replaying from store). Does NOT track changes.
- Projections subscribe to events and build read-optimized views
- Event store is append-only

### Projection Idempotency (CRITICAL)

Projections are replayed on every server startup via `ReplayAll()`. **Every projection must be idempotent** — replaying the same event twice must not create duplicate rows.

- **Never use `uuid.New()` for projection row IDs.** Use deterministic IDs derived from event data: either from `meta.EventID` (passed via `EventMetadata`) or from a stable domain field (e.g., `WalletID`, `TransactionID`).
- When one event produces multiple rows (e.g., `TransactionCompleted` → `transfer_out` + `transfer_in`), use `meta.EventID + "-suffix"` to keep IDs unique and stable.
- Use `ON CONFLICT (id) DO NOTHING` for insert-only projections or `ON CONFLICT (id) DO UPDATE` for mutable projections.
- The `Subscriber.Handle(event any, meta eventstore.EventMetadata)` interface provides `meta.EventID` — use it.

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

- Interface in `shared/eventstore/store.go`: `Append(ctx, aggregateID, events, expectedVersion)`, `Load(ctx, aggregateID)`, `Subscribe(ctx, handler)`
- Optimistic concurrency via `expectedVersion` on Append
- Event envelope: EventID, AggregateID, AggregateType, EventType, Version, CorrelationID, Payload (JSON), OccurredAt
- Build in-memory implementation first, swap in PostgreSQL later

## DDD Rules

- Aggregates protect invariants (e.g., "balance cannot go negative") — all validation in aggregate methods, zero in handlers
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
3. **Projection tests**: Given events, verify read model. Only when projection has logic beyond simple mapping.

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
go vet ./...           # vet code
go build ./...         # verify compilation
golangci-lint run      # lint (if installed)
go run ./cmd/server    # start the server
```
