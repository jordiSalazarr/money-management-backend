# TransferMoney Slice

## Aggregate

- **Name:** Transaction
- **ID:** `transaction_id` (UUID, server-generated)
- **Type:** `transaction`

New aggregate with lifecycle status tracking. For now, a single command creates and completes in one step, but the aggregate records both facts (`TransactionCreated` → `TransactionCompleted`) to support future multi-step flows (e.g., pending approval, failure, refunds).

### Status Lifecycle

`CREATED` → `COMPLETED`

(Future: `CREATED` → `FAILED`, `COMPLETED` → `REFUNDED`)

### State

- `id` (string)
- `status` (string): `CREATED`, `COMPLETED`
- `userID`, `fromWalletID`, `toWalletID`, `amount`, `currency`, `reason`

## Command: TransferMoney

| Field          | Type   | Required | Notes                           |
|----------------|--------|----------|---------------------------------|
| transaction_id | UUID   | yes      | Server-generated                |
| user_id        | UUID   | yes      | Hardcoded for now (no auth yet) |
| from_wallet_id | UUID   | yes      | Source wallet                   |
| to_wallet_id   | UUID   | yes      | Destination wallet              |
| amount         | Int    | yes      | Cents. Must be > 0              |
| currency       | String | yes      | Must match both wallets         |
| reason         | String | no       |                                 |

### Invariants

1. `amount` must be > 0
2. `from_wallet_id` must not equal `to_wallet_id`
3. Both wallets must exist
4. `currency` must match source wallet's currency
5. `currency` must match destination wallet's currency
6. Source wallet must have sufficient balance (`balance >= amount`)

### Orchestration

The command handler performs these steps:

1. Validate `amount > 0`, `from != to`, `user_id` present
2. Load source wallet aggregate → call `Withdraw()` (in-memory only, not persisted yet)
3. Load destination wallet aggregate → call `Deposit()` (in-memory only, not persisted yet)
4. Create Transaction aggregate via `transaction.Create()` → emits `TransactionCreated`
5. Call `txnAgg.Complete()` → emits `TransactionCompleted`
6. Persist all events atomically via `store.AppendMulti()`:
   - Source wallet: `MoneyWithdrawn`
   - Destination wallet: `MoneyDeposited`
   - Transaction: `TransactionCreated` + `TransactionCompleted`

If any step (1-5) fails, nothing is persisted. If `AppendMulti` fails (concurrency conflict), nothing is persisted.

## Event: TransactionCreated

| Field          | Type     | Notes                        |
|----------------|----------|------------------------------|
| transaction_id | UUID     | Aggregate ID                 |
| user_id        | UUID     | Who initiated                |
| from_wallet_id | UUID     | Source wallet                |
| to_wallet_id   | UUID     | Destination wallet           |
| amount         | Int      | Cents                        |
| currency       | String   |                              |
| reason         | String   | Empty string if not provided |
| created_at     | DateTime | Server-generated (UTC)       |

## Event: TransactionCompleted

| Field          | Type     | Notes                        |
|----------------|----------|------------------------------|
| transaction_id | UUID     | Aggregate ID                 |
| user_id        | UUID     | Who initiated                |
| from_wallet_id | UUID     | Source wallet                |
| to_wallet_id   | UUID     | Destination wallet           |
| amount         | Int      | Cents                        |
| currency       | String   |                              |
| reason         | String   | Empty string if not provided |
| completed_at   | DateTime | Server-generated (UTC)       |

## Projection: Transactions (new)

New read model built from `TransactionCreated` and `TransactionCompleted` events.

| Field          | Type     | Notes                      |
|----------------|----------|----------------------------|
| transaction_id | UUID     | = aggregate ID             |
| user_id        | UUID     |                            |
| from_wallet_id | UUID     |                            |
| to_wallet_id   | UUID     |                            |
| amount         | Int      | Cents                      |
| currency       | String   |                            |
| reason         | String   |                            |
| status         | String   | CREATED → COMPLETED        |
| created_at     | DateTime | From TransactionCreated    |
| completed_at   | DateTime | From TransactionCompleted  |

## HTTP Endpoint

- **Method:** `POST`
- **Path:** `/transfers`
- **Request body:**

```json
{
  "user_id": "uuid",
  "from_wallet_id": "uuid",
  "to_wallet_id": "uuid",
  "amount": 5000,
  "currency": "EUR",
  "reason": "Savings to cash"
}
```

- **Success:** `201 Created`

```json
{
  "transaction_id": "uuid"
}
```

- **Errors:**
  - `400` — `invalid_amount`, `same_wallet`, `currency_mismatch`, `insufficient_funds`, `missing_user_id`
  - `404` — `wallet_not_found`

## Error Codes

| Code               | HTTP | Message                                        |
|--------------------|------|------------------------------------------------|
| invalid_amount     | 400  | amount must be greater than 0                  |
| same_wallet        | 400  | source and destination wallet must be different |
| currency_mismatch  | 400  | currency does not match wallet currency        |
| insufficient_funds | 400  | insufficient funds                             |
| missing_user_id    | 400  | user_id is required                            |
| wallet_not_found   | 404  | wallet not found                               |
