# RecordRecurringMovement Slice

## Aggregate

- **Name:** RecurringMovement
- **ID:** `recurring_movement_id` (UUID, server-generated)
- **Type:** `recurring_movement`

New aggregate. Tracks the schedule definition and execution history. Knows nothing about wallets.

## Command: RecordRecurringMovement

| Field       | Type     | Required | Notes                                |
|-------------|----------|----------|--------------------------------------|
| wallet_id   | UUID     | yes      | Target wallet                        |
| user_id     | UUID     | yes      | From auth context                    |
| type        | String   | yes      | Enum: `deposit`, `withdrawal`        |
| amount      | Int      | yes      | Cents. Must be > 0 and <= 10_000_000 |
| currency    | String   | yes      | Enum: `EUR`, `USD`                   |
| frequency   | String   | yes      | Enum: `DAILY`, `WEEKLY`, `MONTHLY`   |
| reason      | String   | yes      |                                      |
| start_date  | Date     | yes      | First occurrence (>= today)          |
| end_date    | Date     | no       | Optional. Must be > start_date       |

### Invariants

1. `amount` must be > 0 and <= 10_000_000
2. `currency` must be `EUR` or `USD`
3. `frequency` must be `DAILY`, `WEEKLY`, or `MONTHLY`
4. `type` must be `deposit` or `withdrawal`
5. `reason` must not be empty
6. `start_date` must be >= today
7. `end_date`, if provided, must be > `start_date`
8. `wallet_id` must not be empty
9. `user_id` must not be empty

## Event: RecurringMovementRegistered

| Field                  | Type     | Notes                          |
|------------------------|----------|--------------------------------|
| recurring_movement_id  | UUID     | Aggregate ID                   |
| wallet_id              | UUID     |                                |
| user_id                | UUID     |                                |
| type                   | String   | `deposit` or `withdrawal`      |
| amount                 | Int      | Cents                          |
| currency               | String   |                                |
| frequency              | String   | `DAILY`, `WEEKLY`, `MONTHLY`   |
| reason                 | String   |                                |
| start_date             | Date     |                                |
| end_date               | Date     | Zero value if not provided     |
| registered_at          | DateTime | Server-generated (UTC)         |

## Projection: RecurringMovements

Read model built from `RecurringMovementRegistered` events.

| Field          | Type     | Notes                                     |
|----------------|----------|-------------------------------------------|
| id             | UUID     | = recurring_movement_id                   |
| wallet_id      | UUID     |                                           |
| user_id        | UUID     |                                           |
| type           | String   | `deposit` or `withdrawal`                 |
| amount         | Int      | Cents                                     |
| currency       | String   |                                           |
| frequency      | String   | `DAILY`, `WEEKLY`, `MONTHLY`              |
| reason         | String   |                                           |
| start_date     | Date     |                                           |
| end_date       | Date     | Nullable                                  |
| next_due_date  | Date     | = start_date on creation                  |
| active         | Boolean  | true on creation                          |
| created_at     | DateTime |                                           |

## Infrastructure: Migration

### 006_create_recurring_movements_view.sql

```sql
CREATE TABLE recurring_movements_view (
    id             UUID        PRIMARY KEY,
    wallet_id      UUID        NOT NULL,
    user_id        UUID        NOT NULL,
    type           TEXT        NOT NULL,
    amount         INTEGER     NOT NULL,
    currency       TEXT        NOT NULL,
    frequency      TEXT        NOT NULL,
    reason         TEXT        NOT NULL DEFAULT '',
    start_date     DATE        NOT NULL,
    end_date       DATE,
    next_due_date  DATE        NOT NULL,
    active         BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_recurring_movements_due ON recurring_movements_view (next_due_date) WHERE active = true;
CREATE INDEX idx_recurring_movements_wallet ON recurring_movements_view (wallet_id);
```

## HTTP Endpoint

### Record a recurring movement

- **Method:** `POST`
- **Path:** `/recurring-movements`
- **Request body:**

```json
{
  "wallet_id": "uuid",
  "type": "deposit",
  "amount": 1000,
  "currency": "EUR",
  "frequency": "MONTHLY",
  "reason": "Salary",
  "start_date": "2026-05-01",
  "end_date": "2027-05-01"
}
```

- **Success:** `201 Created`

```json
{
  "recurring_movement_id": "generated-uuid"
}
```

- **Errors:**
  - `400` — `invalid_amount`, `invalid_currency`, `invalid_frequency`, `invalid_type`, `missing_reason`, `missing_wallet_id`, `invalid_start_date`, `invalid_end_date`

## Error Codes

| Code               | HTTP | Message                                       |
|--------------------|------|-----------------------------------------------|
| invalid_amount     | 400  | amount must be between 1 and 10000000         |
| invalid_currency   | 400  | currency must be EUR or USD                   |
| invalid_frequency  | 400  | frequency must be DAILY, WEEKLY, or MONTHLY   |
| invalid_type       | 400  | type must be deposit or withdrawal            |
| missing_reason     | 400  | reason is required                            |
| missing_wallet_id  | 400  | wallet_id is required                         |
| invalid_start_date | 400  | start_date must be today or in the future     |
| invalid_end_date   | 400  | end_date must be after start_date             |
