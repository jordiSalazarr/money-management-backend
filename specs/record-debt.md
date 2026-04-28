# RecordDebt Slice

## Aggregate

- **Name:** Debt
- **ID:** `debt_id` (UUID, server-generated)
- **Type:** `debt`

## Command: RecordDebt

| Field     | Type   | Required | Notes                          |
|-----------|--------|----------|--------------------------------|
| debt_id   | UUID   | yes      | Server-generated               |
| wallet_id | UUID   | no       | Optional — general debt if absent |
| owner_id  | UUID   | yes      | Hardcoded for now (no auth yet) |
| amount    | Int    | yes      | Cents. Must be > 0 and <= 10_000_000 |
| currency  | String | yes      | Enum: `EUR`, `USD`             |
| direction | String | yes      | Enum: `IN`, `OUT`              |
| reason    | String | yes      |                                |

### Invariants

1. `amount` must be > 0 and <= 10_000_000
2. `currency` must be `EUR` or `USD`
3. `direction` must be `IN` or `OUT`
4. `reason` must not be empty

## Event: DebtRecorded

| Field      | Type     | Notes                          |
|------------|----------|--------------------------------|
| debt_id    | UUID     | Aggregate ID                   |
| wallet_id  | UUID     | Empty string if not provided   |
| owner_id   | UUID     |                                |
| amount     | Int      | Cents                          |
| currency   | String   |                                |
| direction  | String   | `IN` or `OUT`                  |
| reason     | String   |                                |
| created_at | DateTime | Server-generated (UTC)         |

## Projection: Debts

Read model built from `DebtRecorded` events.

| Field      | Type     | Notes                          |
|------------|----------|--------------------------------|
| id         | UUID     | = debt_id                      |
| owner_id   | UUID     |                                |
| wallet_id  | UUID     | Nullable — null if general debt |
| amount     | Int      | Cents                          |
| currency   | String   |                                |
| direction  | String   | `IN` or `OUT`                  |
| reason     | String   |                                |
| status     | String   | Defaults to `created`          |
| created_at | DateTime |                                |

## HTTP Endpoints

### Record a debt

- **Method:** `POST`
- **Path:** `/debts`
- **Request body:**

```json
{
  "owner_id": "uuid",
  "wallet_id": "uuid-or-omitted",
  "amount": 1000,
  "currency": "EUR",
  "direction": "IN",
  "reason": "Dinner split"
}
```

- **Success:** `201 Created`

```json
{
  "debt_id": "generated-uuid"
}
```

- **Errors:**
  - `400` — `invalid_amount`, `invalid_currency`, `invalid_direction`, `missing_reason`, `missing_owner_id`

### List debts

- **Method:** `GET`
- **Path:** `/debts?owner_id=...&wallet_id=...&direction=...&status=...`
- **Query params:**
  - `owner_id` (required)
  - `wallet_id` (optional filter)
  - `direction` (optional filter: `IN` or `OUT`)
  - `status` (optional filter: `created`)
- **Success:** `200 OK`

```json
{
  "debts": [
    {
      "id": "uuid",
      "owner_id": "uuid",
      "wallet_id": "uuid-or-null",
      "amount": 1000,
      "currency": "EUR",
      "direction": "IN",
      "reason": "Dinner split",
      "status": "created",
      "created_at": "2026-04-28T12:00:00Z"
    }
  ]
}
```

- **Errors:**
  - `400` — `missing_owner_id`

## Error Codes

| Code             | HTTP | Message                                     |
|------------------|------|-----------------------------------------------|
| invalid_amount   | 400  | amount must be between 1 and 10000000         |
| invalid_currency | 400  | currency must be EUR or USD                   |
| invalid_direction| 400  | direction must be IN or OUT                   |
| missing_reason   | 400  | reason is required                            |
| missing_owner_id | 400  | owner_id is required                          |
