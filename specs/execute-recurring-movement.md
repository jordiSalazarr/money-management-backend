# ExecuteRecurringMovement Slice

## Aggregate

- **Name:** RecurringMovement
- **ID:** `recurring_movement_id` (UUID)
- **Type:** `recurring_movement`

This slice operates on the existing RecurringMovement aggregate. It adds an `Execute()` method.

## Command: ExecuteRecurringMovement (internal — processor only, no HTTP endpoint)

| Field                  | Type | Required | Notes                              |
|------------------------|------|----------|------------------------------------|
| recurring_movement_id  | UUID | yes      | From processor read model query    |
| executed_for_date      | Date | yes      | The date this execution covers (today) |

### Invariants

1. Recurring movement must be active
2. `last_executed_date` must be < `executed_for_date` (prevents double execution for the same day)

## Event: RecurringMovementExecuted

| Field                  | Type     | Notes                          |
|------------------------|----------|--------------------------------|
| recurring_movement_id  | UUID     | Aggregate ID                   |
| executed_for_date      | Date     | The date covered               |
| executed_at            | DateTime | Server-generated (UTC)         |

## Projection: RecurringMovements (existing)

`RecurringMovementExecuted` updates the existing RecurringMovements read model:

- Advance `next_due_date` based on `frequency`:
  - `DAILY` → `executed_for_date + 1 day`
  - `WEEKLY` → `executed_for_date + 7 days`
  - `MONTHLY` → `executed_for_date + 1 month`
- If `end_date` is set and new `next_due_date > end_date`, set `active = false`

Always advances relative to `executed_for_date`, not the old `next_due_date`. Missed periods are skipped, not backfilled.

## Outbox: Wallet Reaction

When `RecurringMovementExecuted` is persisted, an outbox entry is written **in the same PG transaction** via `AppendWithOutbox`.

The outbox entry carries:

| Field        | Type     | Notes                                |
|--------------|----------|--------------------------------------|
| id           | UUID     | Outbox entry ID                      |
| event_type   | String   | `RecurringMovementExecuted`          |
| payload      | JSONB    | See below                            |
| created_at   | DateTime |                                      |
| processed_at | DateTime | Null until processed                 |

Outbox payload:

```json
{
  "recurring_movement_id": "uuid",
  "wallet_id": "uuid",
  "user_id": "uuid",
  "type": "deposit",
  "amount": 1000,
  "currency": "EUR",
  "reason": "recurring:uuid"
}
```

The reactor dispatches:
- If `type = deposit` → `DepositMoney` command
- If `type = withdrawal` → `WithdrawMoney` command (category: `Other`)

Same outbox poller as debt settlement. The existing poller routes by `event_type`.

## Processor: RecurringMovement Scheduler

A goroutine in `main.go` on a ticker (every 1 hour).

### Query

Reads from the existing `recurring_movements_view` projection:

```sql
SELECT id, wallet_id, user_id, type, amount, currency, frequency, reason,
       start_date, end_date, next_due_date, active, created_at
FROM recurring_movements_view
WHERE next_due_date <= CURRENT_DATE
  AND active = true
```

### Processing loop

For each due item:

1. Load `RecurringMovement` aggregate from event store
2. Call `agg.Execute(today)`
3. Build outbox entry with wallet command payload
4. Persist `RecurringMovementExecuted` event + outbox entry via `AppendWithOutbox`

### Error handling

- If `Execute()` returns `ErrAlreadyExecutedToday` → skip (idempotent, already processed)
- If `Execute()` returns `ErrNotActive` → skip (cancelled or expired between query and processing)
- If persist fails → log error, continue to next item (retry on next tick)

## Error Codes

| Code                    | —   | Message                                    |
|-------------------------|-----|--------------------------------------------|
| already_executed_today  | —   | recurring movement already executed today  |
| not_active              | —   | recurring movement is not active           |

These are internal errors (no HTTP mapping needed — processor only).
