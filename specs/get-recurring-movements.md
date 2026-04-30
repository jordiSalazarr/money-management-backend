# GetRecurringMovements Slice

## Aggregate

No aggregate — this is a read-only slice.

## Query: GetRecurringMovements

| Field     | Type | Required | Notes         |
|-----------|------|----------|---------------|
| wallet_id | UUID | yes      | From URL path |

Returns all recurring movements (active and inactive) for a given wallet.

## Projection: RecurringMovements (existing)

Reuses the existing `RecurringMovementView` read model and `RecurringMovementStore.List()` with `RecurringMovementFilter{WalletID: id}`.

| Field         | Type      | Notes                                        |
|---------------|-----------|----------------------------------------------|
| id            | UUID      | Recurring movement ID                        |
| wallet_id     | UUID      | Wallet this movement belongs to              |
| user_id       | UUID      | Owner                                        |
| type          | String    | `deposit` or `withdrawal`                    |
| amount        | Int       | Positive integer cents                       |
| currency      | String    |                                              |
| frequency     | String    | `DAILY`, `WEEKLY`, `MONTHLY`                 |
| reason        | String    |                                              |
| start_date    | DateTime  |                                              |
| end_date      | DateTime  | Nullable — null means indefinite             |
| next_due_date | DateTime  | Next scheduled execution                     |
| active        | Bool      | False when end_date passed or manually stopped |
| created_at    | DateTime  |                                              |

## HTTP Endpoint

- **Method:** `GET`
- **Path:** `/wallets/:id/recurring-movements`
- **Success:** `200 OK`

```json
{
  "recurring_movements": [
    {
      "id": "uuid",
      "wallet_id": "uuid",
      "user_id": "uuid",
      "type": "deposit",
      "amount": 150000,
      "currency": "EUR",
      "frequency": "MONTHLY",
      "reason": "Salary",
      "start_date": "2026-01-01T00:00:00Z",
      "end_date": null,
      "next_due_date": "2026-05-01T00:00:00Z",
      "active": true,
      "created_at": "2025-12-20T10:00:00Z"
    },
    {
      "id": "uuid",
      "wallet_id": "uuid",
      "user_id": "uuid",
      "type": "withdrawal",
      "amount": 5000,
      "currency": "EUR",
      "frequency": "DAILY",
      "reason": "Coffee subscription",
      "start_date": "2026-03-01T00:00:00Z",
      "end_date": "2026-06-01T00:00:00Z",
      "next_due_date": "2026-05-01T00:00:00Z",
      "active": true,
      "created_at": "2026-02-28T09:00:00Z"
    }
  ]
}
```

- **Errors:**
  - `404` — `wallet_not_found` (wallet does not exist)

## Error Codes

| Code             | HTTP | Message          |
|------------------|------|------------------|
| wallet_not_found | 404  | wallet not found |

## Slice Structure

```
recurringmovement/
  getrecurringmovements/
    handler.go          # HTTP handler — parses wallet_id, calls store.List()
    handler_test.go     # HTTP contract test with in-memory store
    errors.go           # Sentinel: ErrWalletNotFound
    register.go         # RegisterQueries(router, recurringMovementStore, walletStore)
```

## Notes

- The handler needs access to `walletStore` (or equivalent) to verify the wallet exists before querying. If the wallet doesn't exist, return 404. If it exists but has no recurring movements, return 200 with an empty array.
- No new projection or migration needed — reuses existing `RecurringMovementStore.List()`.
- Results are returned unsorted by the store; handler sorts by `created_at` descending (newest first).

## Frontend

- Inside the wallet detail view, add a **"Show recurring movements"** button.
- Clicking it opens a popup/modal that calls `GET /wallets/:id/recurring-movements`.
- The modal displays a list of recurring movements with type, amount, frequency, reason, next due date, and active status.
- Empty state: "No recurring movements for this wallet."
