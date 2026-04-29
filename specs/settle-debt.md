# SettleDebt Slice

## Aggregate

- **Name:** Debt
- **ID:** `debt_id` (UUID)
- **Type:** `debt`

This slice operates on the existing Debt aggregate. It adds a `Settle()` method.

## Command: SettleDebt

| Field      | Type   | Required | Notes                         |
|------------|--------|----------|-------------------------------|
| debt_id    | UUID   | yes      | From URL path                 |
| settled_by | UUID   | yes      | User settling the debt        |

### Invariants

1. Debt must exist (aggregate must have events)
2. Debt must not already be settled (status must be `created`)

## Event: DebtSettled

| Field      | Type     | Notes                    |
|------------|----------|--------------------------|
| debt_id    | UUID     | Aggregate ID             |
| settled_by | UUID     | Who settled              |
| settled_at | DateTime | Server-generated (UTC)   |

## Projection: Debts (existing)

`DebtSettled` updates the existing Debts read model:

- `status` changes from `created` to `settled`

No new projection — just a new event handler on the existing Debts projector.

## Outbox: Wallet Reaction

When `DebtSettled` is persisted, an outbox entry is written **in the same PG transaction**. A poller processes it and dispatches:

- If debt `direction = IN` → `DepositMoney` command on the debt's `wallet_id` (someone owed you, money comes in)
- If debt `direction = OUT` → `WithdrawMoney` command on the debt's `wallet_id` (you owed someone, money goes out)

`wallet_id` is always present (required on debt creation).

The outbox entry carries:

| Field      | Type   | Notes                                |
|------------|--------|--------------------------------------|
| id         | UUID   | Outbox entry ID                      |
| event_type | String | `DebtSettled`                        |
| payload    | JSONB  | `{debt_id, wallet_id, amount, currency, direction, settled_by}` |
| created_at | DateTime | When the entry was created         |
| processed_at | DateTime | Null until processed             |

### Idempotency

The wallet deposit/withdrawal uses the `debt_id` as the `reason` prefix (e.g., `"debt_settlement:<debt_id>"`). The poller marks the outbox entry as processed after successful dispatch. Retries are safe because:

- If the wallet command succeeds but marking processed fails → next poll retries → wallet gets a duplicate deposit/withdrawal. To prevent this, the outbox entry ID is checked before dispatching.
- Future enhancement: add a `settlement_id` field to wallet events for true idempotency at the aggregate level.

### Error handling

- If wallet not found → log error, leave outbox unprocessed for retry
- If insufficient funds (withdrawal) → log error, leave unprocessed for retry
- Max retries could be added later (dead-letter pattern)

## Infrastructure: Outbox Table

### Migration: 005_create_outbox_table.sql

```sql
CREATE TABLE outbox (
    id           UUID        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX idx_outbox_unprocessed ON outbox (created_at) WHERE processed_at IS NULL;
```

### Outbox Poller

- Runs as a goroutine in `main.go` on a ticker (e.g., every 2 seconds)
- Queries: `SELECT * FROM outbox WHERE processed_at IS NULL ORDER BY created_at LIMIT 10`
- For each entry, dispatches the appropriate wallet command
- On success, sets `processed_at = NOW()`
- On failure, logs and moves to next entry (will retry on next tick)

## Infrastructure: AppendWithOutbox

New method on `PostgresStore` (or extend `Append`):

```go
func (s *PostgresStore) AppendWithOutbox(ctx context.Context, aggregateID string, events []Event, expectedVersion int, outboxEntries []OutboxEntry) error
```

Same as `Append` but also inserts outbox entries in the same transaction.

## HTTP Endpoint

### Settle a debt

- **Method:** `POST`
- **Path:** `/debts/:id/settle`
- **Request body:**

```json
{
  "settled_by": "uuid"
}
```

- **Success:** `200 OK`

```json
{
  "debt_id": "uuid",
  "status": "settled"
}
```

- **Errors:**
  - `400` — `missing_settled_by`, `debt_already_settled`
  - `404` — `debt_not_found`

## Error Codes

| Code                 | HTTP | Message                              |
|----------------------|------|--------------------------------------|
| missing_settled_by   | 400  | settled_by is required               |
| debt_already_settled | 400  | debt is already settled              |
| debt_not_found       | 404  | debt not found                       |
